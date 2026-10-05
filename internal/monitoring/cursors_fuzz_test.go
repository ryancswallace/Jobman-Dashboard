package monitoring

import (
	"encoding/json"
	"testing"
)

// Browse state can originate in persisted cursors; arbitrary data must either
// fail safely or produce a complete identity whose JSON round trip is stable.
func FuzzDecodeCursorIdentity(f *testing.F) {
	for _, seed := range [][]byte{nil, []byte(`{}`), []byte(`{"accountId":"alice","queryHash":"q","initial":true}`), []byte(`{"accountId":null,"queryHash":17}`)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		id, err := DecodeCursorIdentity(data)
		if err != nil {
			return
		}
		if len(data) > 4<<20 || id.AccountID == "" || id.QueryHash == "" {
			t.Fatal("accepted incomplete or oversized identity")
		}
		encoded, err := json.Marshal(id)
		if err != nil {
			t.Fatal(err)
		}
		// JSON escaping can enlarge a valid identity beyond the input budget.
		if len(encoded) > 4<<20 {
			return
		}
		recovered, err := DecodeCursorIdentity(encoded)
		if err != nil || recovered != id {
			t.Fatal("identity failed round trip")
		}
	})
}
