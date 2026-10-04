//go:build integration

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestMigrationRehearsalPinsUnifiedBinaryAcrossPhases exercises the real phase
// loop with a fake phase executor, including rejection of a mid-run binary change.
func TestMigrationRehearsalPinsUnifiedBinaryAcrossPhases(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	const exercise = `
import hashlib, importlib.util, json, pathlib, sys, tempfile
from unittest.mock import patch
spec = importlib.util.spec_from_file_location("rehearsal", sys.argv[1])
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
for tamper in (False, True):
    with tempfile.TemporaryDirectory() as temporary:
        root = pathlib.Path(temporary)
        bundle = root / "bundle"
        bundle.mkdir()
        source = root / "source"
        (source / "node1").mkdir(parents=True)
        payload = b"approved unified CLI fixture"
        for name in ("wkcli", "wukongim"):
            (bundle / name).write_bytes(payload)
        plan = root / "plan.json"
        plan.write_text(json.dumps({"sources": [{"data_dir": "/source/node1"}],
            "target": {"nodes": [{"data_dir": "/targets/1"}], "channel_replicas": 1}}))
        output = root / "output"
        checksum = hashlib.sha256(payload).hexdigest()
        argv = [sys.argv[1], "--plan", str(plan), "--bundle", str(bundle),
            "--source-root", str(source), "--output", str(output),
            "--image", "sha256:" + "a" * 64, "--wkcli-sha256", checksum,
            "--wukongim-sha256", checksum]
        seen = []
        def phase_run(args, phase, owner):
            seen.append(phase)
            command = m.command(args, phase, owner)
            entry = command.index("--entrypoint")
            assert command[entry + 1:entry + 4] == ["/bundle/wkcli", args.image, "migrate"], command
            if tamper:
                (bundle / "wkcli").write_bytes(b"changed binary")
            if phase == "export":
                archive = output / "archive/source"
                archive.mkdir()
                (archive / "COMPLETE").touch()
            if phase in ("prepare", "export"):
                return {"selection": {"digest": "selection"}}
            return {"status": "offline_verified" if phase == "verify" else "imported",
                "cutover_ready": False, "nodes": 1, "selection_digest": "selection"}
        with patch.object(sys, "argv", argv), patch.object(m, "phase_run", phase_run), \
             patch.object(m.shutil, "disk_usage", return_value=type("Disk", (), {"free": 10 * 1024**3})()):
            try:
                m.main()
            except RuntimeError as error:
                assert tamper and str(error) == "pinned input changed", error
            else:
                assert not tamper, "binary change was accepted"
        status = json.loads((output / "status.json").read_text())
        assert seen == (["prepare"] if tamper else ["prepare", "export", "import", "retry", "verify"]), seen
        assert status["phase"] == ("stopped" if tamper else "offline_verified"), status
`
	command := exec.Command(python, "-c", exercise, filepath.Join(repoRoot(t), "scripts/migration/rehearse-offline.py"))
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("rehearsal phase loop: %v\n%s", err, output)
	}
}
