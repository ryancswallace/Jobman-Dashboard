package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/store"
)

// This is an actual loopback PostgreSQL wire transport, not a mock SQL call.
// One authenticated query stops returning bytes; pgx must cancel its socket,
// release the pooled connection, and establish a healthy subsequent connection.
func TestDynamicDeadlineCancelsStalledPostgresConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var connections sync.WaitGroup
	var queries atomic.Int32
	stalled, closed := make(chan struct{}), make(chan struct{})
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			go func() {
				defer connections.Done()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				backend := pgproto3.NewBackend(conn, conn)
				message, err := backend.ReceiveStartupMessage()
				if err != nil {
					return
				}
				if _, ok := message.(*pgproto3.StartupMessage); !ok {
					return // pgx may send a separate cancellation request.
				}
				backend.Send(&pgproto3.AuthenticationOk{})
				backend.Send(&pgproto3.ParameterStatus{Name: "server_version", Value: "17.6"})
				backend.Send(&pgproto3.ParameterStatus{Name: "client_encoding", Value: "UTF8"})
				backend.Send(&pgproto3.BackendKeyData{ProcessID: 1, SecretKey: []byte{0, 0, 0, 1}})
				backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
				if backend.Flush() != nil {
					return
				}
				for {
					message, err := backend.Receive()
					if err != nil {
						return
					}
					switch message := message.(type) {
					case *pgproto3.Query:
						if strings.Contains(message.String, "deadline_probe") && queries.Add(1) == 1 {
							close(stalled)
							// Do not manufacture a SQL error or response. pgx retires
							// canceled connections by sending Terminate before closing;
							// acknowledge that by closing this server connection.
							if message, err := backend.Receive(); err != nil {
								return
							} else if _, ok := message.(*pgproto3.Terminate); !ok {
								return
							}
							close(closed)
							return
						}
						backend.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")})
						backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
						if backend.Flush() != nil {
							return
						}
					case *pgproto3.Terminate:
						return
					default:
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-acceptDone
		connections.Wait()
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	db, err := store.Open(ctx, "postgres://synthetic@"+listener.Addr().String()+"/synthetic?sslmode=disable")
	if err != nil {
		t.Fatal("wire fixture startup failed:", err)
	}
	defer db.Close()
	s := &Server{Auth: AuthFunc(func(r *http.Request) (monitoring.Actor, error) {
		_, err := db.Pool.Exec(r.Context(), "SELECT 1 /* deadline_probe */")
		return monitoring.Actor{Account: api.Account{ID: "synthetic"}}, err
	})}
	server := httptest.NewServer(s.handler(100 * time.Millisecond))
	defer server.Close()
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(server.URL + "/api/v1/missing")
	if err != nil {
		t.Fatal("stalled SQL did not produce real HTTP unavailability:", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 503 {
		t.Fatal("stalled SQL returned a misleading status")
	}
	select {
	case <-stalled:
	default:
		t.Fatal("SQL transport was never stalled")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("pgx did not close the stalled connection")
	}
	response, err = client.Get(server.URL + "/api/v1/missing")
	if err != nil {
		t.Fatal("healthy follow-up could not acquire a database connection:", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 404 || queries.Load() != 2 || db.Pool.Stat().AcquiredConns() != 0 {
		t.Fatal("canceled SQL retained a connection or blocked the next request")
	}
}
