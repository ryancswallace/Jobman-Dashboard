//go:build integration

package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

// Explicitly stops only the exact isolated secondary Control, then restores it.
// No LDAP, primary Control, Dashboard, broker, configuration, grant or hold is
// changed. A root-private receipt precedes the stop and survives test failure.
func TestLabMultiSourceSecondaryOutage(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_MULTISOURCE") != "1" || os.Getenv("JOBMAN_DASHBOARD_LAB_MULTISOURCE_OUTAGE") != "1" {
		t.Skip("reviewed multi-source deployment and explicit secondary outage opt-in required")
	}
	alice := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	fixtures := map[string]labMultiFixture{
		labDeployment:          labReadMultiFixture(t, filepath.Join(alice.root, ".lab/dashboard/fixture-info.json"), false),
		labSecondaryDeployment: labReadMultiFixture(t, os.Getenv("JOBMAN_DASHBOARD_LAB_SECONDARY_FIXTURE"), true),
	}
	var scopes []api.Scope
	allowed := labMultiScopes(fixtures, "alice")
	for _, deployment := range []string{labDeployment, labSecondaryDeployment} {
		for _, ns := range fixtures[deployment].Namespaces {
			if ns.Name == "dashboard-research" {
				scopes = append(scopes, api.Scope{DeploymentID: deployment, NamespaceID: ns.ID})
			}
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	request := labReportClient(t, ctx, alice)
	read := func(path string, target any, status int) {
		t.Helper()
		request("GET", path, alice.accessToken, "", nil, target, status)
	}
	var baseline api.Page[api.Job]
	read("/api/v1/jobs?"+labMultiQuery(scopes, 50, ""), &baseline, 200)
	if baseline.Completeness != "complete" {
		t.Fatal("Healthy two-source baseline required before outage")
	}
	labMultiSources(t, baseline.Sources, map[api.Scope]labMultiNamespace{scopes[0]: allowed[scopes[0]], scopes[1]: allowed[scopes[1]]})
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("Cannot identify bounded outage receipt")
	}
	receipt := hex.EncodeToString(nonce[:])
	// Register cleanup before the first remote mutation. A lost stop response
	// still restores only a source proven by the durable pending receipt.
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 60*time.Second)
		defer stop()
		if err := labMultiSourceTransition(cleanup, alice.root, receipt, "start"); err != nil {
			t.Errorf("secondary cleanup failed; inspect private outage receipt %s", receipt)
		}
	})
	if err := labMultiSourceTransition(ctx, alice.root, receipt, "stop"); err != nil {
		t.Fatalf("secondary stop failed; preserve private receipt %s", receipt)
	}
	var partial api.Page[api.Job]
	read("/api/v1/jobs?"+labMultiQuery(scopes, 50, ""), &partial, 200)
	if partial.Completeness != "partial" || len(partial.Items) == 0 {
		t.Fatal("Healthy primary contribution must remain truthfully partial")
	}
	for _, job := range partial.Items {
		if job.Scope != scopes[0] {
			t.Fatal("Unavailable secondary rows were retained or another scope leaked")
		}
	}
	available, unavailable := false, false
	for _, source := range partial.Sources {
		if source.DeploymentID == labDeployment && source.NamespaceID == scopes[0].NamespaceID && source.Status == "available" {
			available = true
		}
		if source.DeploymentID == labSecondaryDeployment && (source.Status == "unavailable" || source.Status == "authorization_unavailable") {
			unavailable = true
		}
	}
	if !available || !unavailable {
		t.Fatal("Source outage provenance was hidden or treated as a healthy zero")
	}
	for _, endpoint := range []string{"jobs", "overview", "targets", "workloads/collection", "workloads/array", "workloads/graph"} {
		read("/api/v1/"+endpoint+"?"+labMultiQuery(scopes[1:], 0, ""), nil, 503)
	}
	read(labMultiPrefix(scopes[1])+"/jobs/"+allowed[scopes[1]].JobIDs[0], nil, 503)
	var overview api.Overview
	read("/api/v1/overview?"+labMultiQuery(scopes, 0, ""), &overview, 200)
	if overview.Completeness != "partial" || overview.Active == nil || overview.AwaitingExecution == nil {
		t.Fatal("Healthy overview subtotal was not identified as partial")
	}
	var targets api.TargetPage
	read("/api/v1/targets?"+labMultiQuery(scopes, 50, ""), &targets, 200)
	if targets.Completeness != "partial" || len(targets.Items) == 0 {
		t.Fatal("Target contribution did not survive isolated outage")
	}
	for _, target := range targets.Items {
		if target.Scope != scopes[0] {
			t.Fatal("Unavailable target source leaked cached rows")
		}
	}
	for _, kind := range []string{"collection", "array", "graph"} {
		var page api.WorkloadPage
		read("/api/v1/workloads/"+kind+"?"+labMultiQuery(scopes, 50, ""), &page, 200)
		if page.Completeness != "partial" || len(page.Items) == 0 {
			t.Fatal("Workload contribution did not survive isolated outage")
		}
		for _, item := range page.Items {
			if item.Scope != scopes[0] {
				t.Fatal("Unavailable workload source leaked cached rows")
			}
		}
	}
	if err := labMultiSourceTransition(ctx, alice.root, receipt, "start"); err != nil {
		t.Fatalf("secondary restore failed; preserve private receipt %s", receipt)
	}
	labActualPoll(t, ctx, 45*time.Second, func() bool {
		var restored api.Page[api.Job]
		read("/api/v1/jobs?"+labMultiQuery(scopes, 50, ""), &restored, 200)
		if restored.Completeness != "complete" {
			return false
		}
		labMultiSources(t, restored.Sources, map[api.Scope]labMultiNamespace{scopes[0]: allowed[scopes[0]], scopes[1]: allowed[scopes[1]]})
		seen := map[api.Scope]bool{}
		for _, job := range restored.Items {
			seen[job.Scope] = true
		}
		return seen[scopes[0]] && seen[scopes[1]]
	})
	t.Logf("PASS: exact isolated secondary source stopped/restored, primary catalogs remained partial and authorized, secondary-only reads failed unavailable, both recovered; durable private receipt %s", receipt)
}

func labMultiSourceTransition(ctx context.Context, root, receipt, action string) error {
	if action != "start" && action != "stop" || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(receipt) || !filepath.IsAbs(root) {
		return fmt.Errorf("invalid scoped source transition")
	}
	var inventory map[string]struct {
		Host string `json:"ansible_host"`
		User string `json:"ansible_user"`
		Port int    `json:"ansible_port"`
		Key  string `json:"ansible_ssh_private_key_file"`
	}
	file, err := os.Open(filepath.Join(root, ".lab/dashboard/ssh-connections.json"))
	if err != nil {
		return fmt.Errorf("pinned inventory unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 65537))
	_ = file.Close()
	if err != nil || len(raw) > 65536 || json.Unmarshal(raw, &inventory) != nil {
		return fmt.Errorf("pinned inventory invalid")
	}
	host := inventory["control01"]
	ip := net.ParseIP(host.Host)
	_, subnet, _ := net.ParseCIDR("10.211.55.0/24")
	if host.User != "vagrant" || ip == nil || !(ip.IsLoopback() || ip.Equal(net.ParseIP("10.77.0.21")) || subnet.Contains(ip)) || host.Port < 1 || host.Port > 65535 || !filepath.IsAbs(host.Key) {
		return fmt.Errorf("fixed source guest differs")
	}
	const script = `import fcntl,hashlib,json,os,re,stat,subprocess,sys,time
from pathlib import Path
os.umask(0o077)
p=json.load(sys.stdin);assert os.geteuid()==0 and os.uname().nodename.split('.')[0]=='control01' and p['action'] in ('start','stop') and re.fullmatch('[0-9a-f]{32}',p['receipt'])
base=Path('/var/lib/jobman-dashboard-multisource-outage');root=base/p['receipt'];unit='jobman-dashboard-lab-control-secondary';exe='/usr/local/libexec/jobman-dashboard-secondary/jobman-control';source=Path('/etc/jobman-dashboard-secondary/control')
def read(path,uid=0,mode=0o600,maximum=1048576):
 assert path.parent.resolve()==path.parent
 fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
 with os.fdopen(fd,'rb') as f:
  a=os.fstat(f.fileno());assert stat.S_ISREG(a.st_mode) and a.st_uid==a.st_gid==uid and a.st_nlink==1 and stat.S_IMODE(a.st_mode)==mode and 0<a.st_size<=maximum
  data=f.read(maximum+1);b=os.fstat(f.fileno());c=path.lstat();assert len(data)==a.st_size and (a.st_dev,a.st_ino,a.st_size,a.st_mtime_ns,a.st_ctime_ns)==(b.st_dev,b.st_ino,b.st_size,b.st_mtime_ns,b.st_ctime_ns) and (a.st_dev,a.st_ino)==(c.st_dev,c.st_ino);return data
def sha(data):return hashlib.sha256(data).hexdigest()
def sync(path):
 fd=os.open(path,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
 try:os.fsync(fd)
 finally:os.close(fd)
def put(path,value):
 fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
 with os.fdopen(fd,'w') as f:os.fchmod(f.fileno(),0o600);json.dump(value,f,sort_keys=True);f.flush();os.fsync(f.fileno())
 sync(path.parent)
def directory(path,create):
 if create and not path.exists():path.mkdir(mode=0o700);path.chmod(0o700);sync(path.parent)
 a=path.lstat();assert path.resolve()==path and stat.S_ISDIR(a.st_mode) and a.st_uid==a.st_gid==0 and stat.S_IMODE(a.st_mode)==0o700
def run(args,seconds=5):
 v=subprocess.run(args,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,timeout=seconds,check=True);assert len(v.stdout)<65536;return v.stdout.decode().strip()
def active(name):return subprocess.run(['systemctl','is-active','--quiet',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=5).returncode==0
def process(name):
 assert active(name)
 pid=run(['systemctl','show',name,'--property=MainPID','--value']);assert pid.isdigit() and int(pid)>0
 return {'pid':pid,'uid':os.stat('/proc/'+pid).st_uid,'exe':os.readlink('/proc/'+pid+'/exe'),'start':run(['systemctl','show',name,'--property=ExecMainStartTimestampMonotonic','--value'])}
def unchanged():return {name:process(name) for name in ('jobman-control','jobman-dashboard-lab-control','jobman-dashboard-lab-directory','jobman-dashboard-lab-directory-secondary','jobman-keycloak','jobman-dashboard-lab-broker')}
def pin():
 assert sha(read(Path(exe),0,0o755,64<<20))=='38d5d71cadfbde8147d95ca89aa65a5c8783d983c0b5011055d9b08a0e927e7e'
 text=read(Path('/etc/systemd/system')/(unit+'.service'),0,0o644).decode()
 assert 'ExecStart='+exe+'\n' in text and 'User=jobman-dashboard-source2\n' in text and 'EnvironmentFile='+str(source/'control.env')+'\n' in text
 return {'unit':sha(text.encode()),'environment':sha(read(source/'control.env',21907)),'ca':sha(read(source/'fixture-ca.crt',21907))}
directory(base,p['action']=='stop');directory(root,p['action']=='stop')
lock=base/'.lock'
try:fd=os.open(lock,os.O_RDWR|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600);os.fchmod(fd,0o600)
except FileExistsError:fd=os.open(lock,os.O_RDWR|os.O_NOFOLLOW|os.O_NONBLOCK)
a=os.fstat(fd);assert stat.S_ISREG(a.st_mode) and a.st_nlink==1 and a.st_uid==a.st_gid==0 and stat.S_IMODE(a.st_mode)==0o600
fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
if p['action']=='stop':
 assert not (root/'pending.json').exists() and not any(base.glob('*/pending.json'))
 current=process(unit);assert current['uid']==21907 and current['exe']==exe
 value={'receipt':p['receipt'],'source':current,'pins':pin(),'preserved':unchanged()};put(root/'pending.json',value)
 run(['systemctl','stop',unit],20);assert not active(unit) and unchanged()==value['preserved'];put(root/'stopped.json',{'stopped':True})
else:
 value=json.loads(read(root/'pending.json')) if (root/'pending.json').exists() else json.loads(read(root/'restored.json'))
 assert value['receipt']==p['receipt'] and pin()==value['pins'] and unchanged()==value['preserved']
 if not active(unit):run(['systemctl','start',unit],20)
 deadline=time.monotonic()+20
 while True:
  try:
   current=process(unit);assert current['uid']==21907 and current['exe']==exe
   cap=json.loads(run(['runuser','-u','jobman-dashboard-source2','--','curl','--silent','--fail','--max-time','2','--cacert',str(source/'fixture-ca.crt'),'https://127.0.0.1:28443/v1/capabilities'],3))['capabilities']
   assert cap['instanceId']=='a4f0e2ab-7323-4c90-9510-1f073c660f06' and cap['recoveryEpoch']=='1';break
  except Exception:
   assert time.monotonic()<deadline;time.sleep(0.2)
 assert unchanged()==value['preserved']
 if not (root/'restored.json').exists():put(root/'restored.json',value)
 if (root/'pending.json').exists():os.rename(root/'pending.json',root/'completed-pending.json');sync(root)
print('verified-secondary-'+p['action'])
`
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("SSH unavailable")
	}
	quoted := "'" + strings.ReplaceAll(script, "'", "'\\''") + "'"
	command := exec.CommandContext(ctx, ssh, "-i", host.Key, "-p", fmt.Sprint(host.Port), "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "ConnectTimeout=10", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile="+filepath.Join(root, ".lab/dashboard/known_hosts"), "-o", "HostKeyAlgorithms=ssh-ed25519", host.User+"@"+host.Host, "sudo python3 -c "+quoted)
	payload, _ := json.Marshal(map[string]string{"receipt": receipt, "action": action})
	command.Stdin = bytes.NewReader(payload)
	command.Stderr = io.Discard
	var output labExecutionOutput
	command.Stdout = &output
	if command.Run() != nil || strings.TrimSpace(string(output.Bytes())) != "verified-secondary-"+action {
		return fmt.Errorf("scoped secondary transition failed")
	}
	return nil
}
