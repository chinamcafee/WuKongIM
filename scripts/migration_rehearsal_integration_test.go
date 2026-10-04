//go:build integration && (linux || darwin)

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMigrationRehearsalLifecycle exercises real process cancellation against a
// controlled Docker CLI, including a daemon start that settles after cancellation.
func TestMigrationRehearsalLifecycle(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	for _, phase := range []string{"create", "start", "success"} {
		t.Run(phase, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			if err := os.Mkdir(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			fake := `#!/usr/bin/env python3
import json, os, pathlib, sys, time
root=pathlib.Path(os.environ['WK_TEST_DOCKER_STATE'])
state=root/'container.json'
a=sys.argv[1:]
identity='a'*64
owner='rehearsal-lifecycle-test'
def write(running):
    p=state.with_suffix('.pending')
    p.write_text(json.dumps({'Id':identity,'Config':{'Labels':{'wkmigrate.rehearsal':owner}},'State':{'Running':running}}))
    p.replace(state)
if a[0] in ('create','run'):
    if a[0]=='create': write(False)
    (root/'create.marker').touch()
    time.sleep(0.3)
    if a[0]=='run':
        write(True)
        (root/'workload.marker').touch()
    print(identity if a[0]=='create' else '{}',flush=True)
elif a[0]=='start':
    (root/'start.marker').touch()
    time.sleep(0.3)
    write(True)
    (root/'workload.marker').touch()
    print('{}',flush=True)
elif a[0]=='inspect':
    if not state.exists(): sys.exit(1)
    print('['+state.read_text()+']')
elif a[0]=='stop':
    write(False)
    (root/'stopped.marker').touch()
elif a[0]=='rm':
    assert not json.loads(state.read_text())['State']['Running']
    state.unlink()
    (root/'removed.marker').touch()
else: sys.exit(2)
`
			driver := `import importlib.util, pathlib, signal, sys
from types import SimpleNamespace
spec=importlib.util.spec_from_file_location('rehearsal',sys.argv[1])
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
root=pathlib.Path(sys.argv[2]);output=root/'output';output.mkdir()
def interrupted(signum,frame): raise KeyboardInterrupt('test cancellation')
signal.signal(signal.SIGTERM,interrupted)
a=SimpleNamespace(output=output,bundle=root,source_root=root,artifact_root=None,image='sha256:'+'b'*64,disk_reserve_gib=1,phase_timeout_seconds=10)
# Disk capacity is not this test's variable; keep its production guard enabled.
m.shutil.disk_usage=lambda path:SimpleNamespace(free=8*1024**3)
m.phase_run(a,'prepare','rehearsal-lifecycle-test')
`
			for name, body := range map[string]string{filepath.Join(bin, "docker"): fake, filepath.Join(root, "driver.py"): driver} {
				if err := os.WriteFile(name, []byte(body), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			log, err := os.Create(filepath.Join(root, "driver.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			cmd := exec.Command(python, filepath.Join(root, "driver.py"), filepath.Join(repoRoot(t), "scripts/migration/rehearse-offline.py"), root)
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "WK_TEST_DOCKER_STATE="+root, "PYTHONDONTWRITEBYTECODE=1")
			cmd.Stdout = log
			cmd.Stderr = log
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			defer cmd.Process.Kill()
			if phase != "success" {
				deadline := time.Now().Add(5 * time.Second)
				for {
					if _, err := os.Stat(filepath.Join(root, phase+".marker")); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("Docker lifecycle marker deadline")
					}
					time.Sleep(5 * time.Millisecond)
				}
				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if (err != nil) != (phase != "success") {
					body, _ := os.ReadFile(log.Name())
					t.Fatalf("unexpected exit: %v: %s", err, body)
				}
			case <-time.After(8 * time.Second):
				t.Fatal("cleanup deadline")
			}
			if _, err := os.Stat(filepath.Join(root, "container.json")); !os.IsNotExist(err) {
				t.Fatal("owned container remains after cleanup")
			}
			if _, err := os.Stat(filepath.Join(root, "removed.marker")); err != nil {
				t.Fatal("owned container was not removed")
			}
			if phase == "create" {
				if _, err := os.Stat(filepath.Join(root, "workload.marker")); !os.IsNotExist(err) {
					t.Fatal("interrupted creation started workload")
				}
			} else if _, err := os.Stat(filepath.Join(root, "stopped.marker")); err != nil {
				t.Fatal("late workload start was not stopped")
			}
			body, err := os.ReadFile(filepath.Join(root, "output/prepare.execution.json"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), `"cleanup": "removed"`) {
				t.Fatalf("cleanup was not confirmed: %s", body)
			}
		})
	}
}
