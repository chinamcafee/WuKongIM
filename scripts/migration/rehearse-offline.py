#!/usr/bin/env python3
"""Rehearse a pinned Linux migration package in new, isolated directories."""

import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import signal
import subprocess
import time
import uuid


def digest(path):
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()


def atomic(path, value):
    pending = path.with_suffix(path.suffix + ".pending")
    pending.write_text(json.dumps(value, indent=2) + "\n")
    pending.replace(path)


def under(path, prefix):
    """Require normalized container paths; never rewrite business decisions."""
    parsed = PurePosixPath(path)
    if str(parsed) != path or ".." in parsed.parts or not parsed.is_relative_to(prefix) or str(parsed) == prefix:
        raise ValueError("plan path must be strictly under " + prefix)
    return parsed.relative_to(prefix)


def overlap(first, second):
    return first == second or first in second.parents or second in first.parents


def inputs(args):
    plan = json.loads(args.plan.read_text())
    if args.output.exists():
        raise ValueError("output must not exist; interrupted evidence is preserved")
    roots = [args.bundle, args.source_root, args.plan]
    if args.artifact_root:
        roots.append(args.artifact_root)
    if any(overlap(args.output, root) for root in roots):
        raise ValueError("output overlaps an input")
    for node in plan["sources"]:
        relative = under(node["data_dir"], "/source")
        source = args.source_root / relative
        if not source.is_dir() or not source.resolve().is_relative_to(args.source_root):
            raise ValueError("source directory missing or escapes read-only root")
    targets = [under(node["data_dir"], "/targets") for node in plan["target"]["nodes"]]
    if len(set(targets)) != len(targets) or any(len(path.parts) != 1 for path in targets):
        raise ValueError("targets must name distinct immediate children of /targets")
    for artifact in plan.get("plugin_artifacts", []):
        relative = under(artifact["path"], "/source-programs")
        if not args.artifact_root or not (args.artifact_root / relative).is_file():
            raise ValueError("approved plugin artifact missing")
        if not (args.artifact_root / relative).resolve().is_relative_to(args.artifact_root):
            raise ValueError("plugin artifact escapes read-only root")
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", args.image):
        raise ValueError("runtime image must be an existing immutable image ID")
    for name in ("wkcli", "wukongim"):
        expected = getattr(args, name + "_sha256")
        if not re.fullmatch(r"[0-9a-f]{64}", expected) or digest(args.bundle / name) != expected:
            raise ValueError(name + " does not match the approved package digest")
    return plan


def mount(host, guest, readonly=False):
    if "," in str(host):
        raise ValueError("Docker bind mount paths cannot contain commas")
    return ["--mount", "type=bind,src=" + str(host) + ",dst=" + guest + (",readonly" if readonly else "")]


def command(args, phase, owner):
    """Import and verification cannot access sources or producer scratch state."""
    workspace = {"prepare": "prepare-work", "export": "prepare-work", "import": "import-work", "retry": "import-work", "verify": "verify-work"}[phase]
    cmd = ["docker", "create", "--name", owner, "--label", "wkmigrate.rehearsal=" + owner,
           "--pull=never", "--platform", "linux/amd64", "--network", "none", "--read-only",
           "--memory", "1536m", "--memory-swap", "1536m", "--cpus", "4", "--pids-limit", "256",
           "--tmpfs", "/tmp:rw,nosuid,nodev,size=64m", "-e", "GOMAXPROCS=4", "-e", "GOMEMLIMIT=512MiB"]
    cmd += mount(args.bundle, "/bundle", True)
    cmd += mount(args.output / "plan.json", "/plan.json", True)
    cmd += mount(args.output / workspace, "/scratch")
    cmd += mount(args.output / "targets", "/targets")
    if phase in ("prepare", "export"):
        cmd += mount(args.source_root, "/source", True)
        if args.artifact_root:
            cmd += mount(args.artifact_root, "/source-programs", True)
    if phase != "prepare":
        cmd += mount(args.output / "archive", "/archive", phase != "export")
    cmd += ["--entrypoint", "/bundle/wkcli", args.image, "migrate",
            "import" if phase == "retry" else phase, "--plan", "/plan.json", "--workspace", "/scratch/workspace"]
    if phase != "prepare":
        cmd += ["--archive", "/archive/source"]
    return cmd


def docker(*args):
    return subprocess.run(["docker", *args], check=True, capture_output=True, text=True, timeout=30)


def owned(owner):
    item = json.loads(docker("inspect", owner).stdout)[0]
    if item["Config"]["Labels"].get("wkmigrate.rehearsal") != owner:
        raise RuntimeError("container ownership differs")
    return item


def phase_run(args, phase, owner):
    cmd = command(args, phase, owner)
    started = time.monotonic()
    guard = None
    process = None
    container_id = None
    cleanup = "not_confirmed"
    with (args.output / (phase + ".stdout.json")).open("wb") as out, (args.output / (phase + ".stderr.log")).open("wb") as err:
        try:
            # Creation cannot execute migration work. Capture ownership before start.
            created = subprocess.run(cmd, check=True, stdout=subprocess.PIPE, stderr=err, text=True, timeout=30)
            container_id = created.stdout.strip()
            if not re.fullmatch(r"[0-9a-f]{64}", container_id):
                raise RuntimeError("Docker did not return a container ID")
            item = owned(owner)
            if item["Id"] != container_id:
                raise RuntimeError("created container identity differs")
            atomic(args.output / (phase + ".created.json"), item)
            process = subprocess.Popen(["docker", "start", "--attach", container_id], stdout=out, stderr=err)
            while process.poll() is None:
                if shutil.disk_usage(args.output).free < args.disk_reserve_gib * 1024**3:
                    guard = "free disk fell below reserve"
                elif time.monotonic() - started > args.phase_timeout_seconds:
                    guard = "phase deadline exceeded"
                if guard:
                    raise RuntimeError(guard)
                time.sleep(2)
            if process.returncode:
                raise RuntimeError("phase failed; inspect " + phase + ".stderr.log")
        finally:
            try:
                if process is not None:
                    # Settle a pending start, then inspect again: it may have become
                    # running after the first stop check. A CLI exit is not cleanup.
                    try:
                        item = owned(owner)
                        if item["Id"] != container_id:
                            raise RuntimeError("container identity differs during cleanup")
                        if item["State"]["Running"]:
                            docker("stop", "--time", "15", container_id)
                    finally:
                        try:
                            process.wait(timeout=30)
                        except subprocess.TimeoutExpired:
                            process.terminate()
                            process.wait(timeout=10)
                item = owned(owner)
                if container_id is not None and item["Id"] != container_id:
                    raise RuntimeError("container identity differs during cleanup")
                if item["State"]["Running"]:
                    docker("stop", "--time", "15", item["Id"])
                item = owned(owner)
                atomic(args.output / (phase + ".container.json"), item)
                docker("rm", item["Id"])
                cleanup = "removed"
            finally:
                atomic(args.output / (phase + ".execution.json"), {
                    "phase": phase, "command": cmd,
                    "start_command": ["docker", "start", "--attach", container_id] if process is not None else None,
                    "exit_code": process.returncode if process is not None else None,
                    "container_id": container_id, "cleanup": cleanup,
                    "seconds": time.monotonic() - started, "guard": guard,
                    "free_disk_bytes": shutil.disk_usage(args.output).free,
                    "source_mounted": phase in ("prepare", "export"),
                })
    return json.loads((args.output / (phase + ".stdout.json")).read_text())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("plan", "bundle", "source-root", "output"):
        parser.add_argument("--" + name, type=lambda value: Path(value).resolve(), required=True)
    parser.add_argument("--artifact-root", type=lambda value: Path(value).resolve())
    for name in ("image", "wkcli-sha256", "wukongim-sha256"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--disk-reserve-gib", type=int, default=2)
    parser.add_argument("--phase-timeout-seconds", type=int, default=5400)
    parser.add_argument("--expected-retained-messages", type=int)
    parser.add_argument("--dry-run", action="store_true", help="validate and show commands without creating directories or starting Docker")
    args = parser.parse_args()
    if args.disk_reserve_gib < 1 or args.phase_timeout_seconds < 1:
        parser.error("disk reserve and timeout must be positive")
    if args.expected_retained_messages is not None and args.expected_retained_messages < 0:
        parser.error("expected retained messages cannot be negative")
    plan = inputs(args)
    phases = ("prepare", "export", "import", "retry", "verify")
    owner = "wkmigrate-" + uuid.uuid4().hex[:16]
    if args.dry_run:
        print(json.dumps({phase: command(args, phase, owner + "-" + phase) for phase in phases}, indent=2))
        return
    if shutil.disk_usage(args.output.parent).free < args.disk_reserve_gib * 1024**3:
        raise RuntimeError("insufficient free disk before starting")
    os.umask(0o077)
    args.output.mkdir(mode=0o700)
    shutil.copyfile(args.plan, args.output / "plan.json")
    plan_hash = digest(args.output / "plan.json")
    for name in ("prepare-work", "archive", "import-work", "verify-work", "targets"):
        (args.output / name).mkdir()
    atomic(args.output / "inputs.json", {"plan_sha256": plan_hash, "image": args.image,
        "wkcli_sha256": args.wkcli_sha256, "wukongim_sha256": args.wukongim_sha256,
        "owner": owner, "performance_acceptance": False})
    results = {}
    def interrupted(signum, frame):
        raise KeyboardInterrupt("rehearsal interrupted; stopping the owned phase")
    signal.signal(signal.SIGTERM, interrupted)
    try:
        for phase in phases:
            if digest(args.output / "plan.json") != plan_hash or digest(args.bundle / "wkcli") != args.wkcli_sha256:
                raise RuntimeError("pinned input changed")
            atomic(args.output / "status.json", {"phase": phase, "updated_at": time.time()})
            result = phase_run(args, phase, owner + "-" + phase)
            results[phase] = result
            if phase == "export" and not (args.output / "archive" / "source" / "COMPLETE").is_file():
                raise RuntimeError("archive has no COMPLETE marker")
            if phase == "export" and result["selection"]["digest"] != results["prepare"]["selection"]["digest"]:
                raise RuntimeError("archive selection differs from prepare")
            if phase in ("import", "retry") and (result.get("status") != "imported" or result.get("cutover_ready") is not False):
                raise RuntimeError("unexpected import result")
            if phase in ("import", "retry", "verify") and result.get("nodes") != len(plan["target"]["nodes"]):
                raise RuntimeError("target node count differs from the plan")
            if phase == "verify":
                if result.get("status") != "offline_verified" or result.get("cutover_ready") is not False:
                    raise RuntimeError("independent verification did not pass")
                if result["selection_digest"] != results["prepare"]["selection"]["digest"]:
                    raise RuntimeError("independent selection differs from prepare")
                if args.expected_retained_messages is not None and result["verified_message_replicas"] != args.expected_retained_messages * plan["target"]["channel_replicas"]:
                    raise RuntimeError("verified message replicas differ from acceptance target")
        atomic(args.output / "status.json", {"phase": "offline_verified", "cutover_ready": False, "updated_at": time.time()})
        print(json.dumps({"status": "offline_verified", "cutover_ready": False, "output": str(args.output)}))
    except BaseException as error:
        atomic(args.output / "status.json", {"phase": "stopped", "error": str(error), "updated_at": time.time()})
        raise


if __name__ == "__main__":
    main()
