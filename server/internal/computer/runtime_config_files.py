# Runtime configuration transport. The caller runs this as the target Linux UID.
# No CLI is executed here. Only structured results, never exception text, leave stdout.
import fcntl
import hashlib
import json
import os
import pwd
import secrets
import stat
import sys

LIMIT = 1048576


def directory(root_fd, parts, create=False):
    fd = os.dup(root_fd)
    try:
        for part in parts:
            if not part or part in (".", ".."):
                raise ValueError("model_config_invalid")
            if create:
                try:
                    os.mkdir(part, 0o700, dir_fd=fd)
                except FileExistsError:
                    pass
            nxt = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
            os.close(fd)
            fd = nxt
        return fd
    except BaseException:
        os.close(fd)
        raise


def read_file(root_fd, path):
    parts = path.split("/")
    try:
        parent = directory(root_fd, parts[:-1])
    except FileNotFoundError:
        return None
    try:
        try:
            fd = os.open(parts[-1], os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=parent)
        except FileNotFoundError:
            return None
        with os.fdopen(fd, "rb") as f:
            info = os.fstat(f.fileno())
            if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_nlink != 1:
                raise ValueError("model_config_invalid")
            data = f.read(LIMIT + 1)
            if len(data) > LIMIT:
                raise ValueError("model_config_invalid")
            return data.decode("utf-8")
    finally:
        os.close(parent)


def snapshot(root_fd, paths):
    files = {p: read_file(root_fd, p) for p in sorted(paths)}
    encoded = json.dumps(files, sort_keys=True, ensure_ascii=True, separators=(",", ":")).encode()
    if len(encoded) > 4194304:
        raise ValueError("model_config_invalid")
    return {"files": files, "revision": hashlib.sha256(encoded).hexdigest()}


def private_write(parent, name, data):
    fd = os.open(name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=parent)
    with os.fdopen(fd, "wb") as f:
        f.write(data)
        f.flush()
        os.fsync(f.fileno())


def content_hash(value):
    return None if value is None else hashlib.sha256(value.encode()).hexdigest()


def recover(root_fd, journal_fd):
    """Recover one interrupted transaction without touching external edits."""
    for name in ("committed.json", "pending.tmp"):
        try:
            os.unlink(name, dir_fd=journal_fd)
        except FileNotFoundError:
            pass
    try:
        fd = os.open("pending.json", os.O_RDONLY | os.O_NOFOLLOW, dir_fd=journal_fd)
    except FileNotFoundError:
        return
    conflict = False
    try:
        try:
            with os.fdopen(fd, "r") as f:
                journal = json.load(f)
            entries = journal.get("entries") if isinstance(journal, dict) else None
            if not isinstance(entries, list) or not isinstance(journal.get("committed", False), bool):
                raise ValueError("invalid journal")
            for entry in entries:
                if not isinstance(entry, dict) or not isinstance(entry.get("path"), str) or not isinstance(entry.get("staged"), str) or not isinstance(entry.get("backup"), str):
                    raise ValueError("invalid journal")
        except (ValueError, UnicodeError, OSError, TypeError, KeyError):
            os.unlink("pending.json", dir_fd=journal_fd)
            os.fsync(journal_fd)
            raise ValueError("model_config_conflict")
        for entry in journal.get("entries", []):
            path = entry.get("path", "")
            parts = path.split("/")
            try:
                parent = directory(root_fd, parts[:-1])
            except (FileNotFoundError, NotADirectoryError):
                conflict = True
                continue
            try:
                name = parts[-1]
                if not journal.get("committed"):
                    try:
                        current_hash = content_hash(read_file(root_fd, path))
                    except (OSError, UnicodeError, ValueError):
                        current_hash = "__invalid__"
                    old_hash, new_hash = entry.get("old_hash"), entry.get("new_hash")
                    if current_hash == old_hash:
                        pass
                    elif current_hash == new_hash:
                        try:
                            if entry.get("existed"):
                                os.replace(entry["backup"], name, src_dir_fd=parent, dst_dir_fd=parent)
                            else:
                                os.unlink(name, dir_fd=parent)
                        except FileNotFoundError:
                            pass
                    else:
                        conflict = True
                # A backup contains the previous private configuration. It is
                # only needed while rolling back an interrupted replacement;
                # retaining it after a conflict would leave a secret-bearing
                # orphan with no recovery path.
                names = (entry.get("backup"), entry.get("staged"))
                for item in names:
                    if not item:
                        continue
                    try:
                        os.unlink(item, dir_fd=parent)
                    except FileNotFoundError:
                        pass
                os.fsync(parent)
            finally:
                os.close(parent)
    finally:
        try:
            os.unlink("pending.json", dir_fd=journal_fd)
        except FileNotFoundError:
            pass
        os.fsync(journal_fd)
    if conflict:
        raise ValueError("model_config_conflict")

def save(root_fd, request, current):
    if request.get("expected_revision") != current["revision"]:
        raise ValueError("model_config_conflict")
    files = request["files"]
    if not files or set(files) - set(request["paths"]):
        raise ValueError("model_config_invalid")
    if request.get("create_only") and any(v is not None for v in current["files"].values()):
        raise ValueError("model_config_exists")
    journal_fd = directory(root_fd, [".multica", "runtime-model-config"], True)
    entries = []
    for path, value in files.items():
        if not isinstance(value, str) or len(value.encode()) > LIMIT or "\x00" in value:
            raise ValueError("model_config_invalid")
        entries.append({"path": path, "existed": current["files"][path] is not None,
                        "staged": ".multica-new-" + secrets.token_hex(16),
                        "backup": ".multica-old-" + secrets.token_hex(16),
                        "old_hash": content_hash(current["files"][path]),
                        "new_hash": content_hash(value)})
    pending = False
    try:
        private_write(journal_fd, "pending.tmp", json.dumps({"entries": entries}).encode())
        os.replace("pending.tmp", "pending.json", src_dir_fd=journal_fd, dst_dir_fd=journal_fd)
        os.fsync(journal_fd)
        pending = True
        for entry in entries:
            parent = directory(root_fd, entry["path"].split("/")[:-1], True)
            try:
                value = files[entry["path"]]
                private_write(parent, entry["staged"], value.encode())
                if entry["existed"]:
                    private_write(parent, entry["backup"], current["files"][entry["path"]].encode())
                os.fsync(parent)
            finally:
                os.close(parent)
        if snapshot(root_fd, request["paths"])["revision"] != current["revision"]:
            raise ValueError("model_config_conflict")
        for entry in entries:
            parent = directory(root_fd, entry["path"].split("/")[:-1])
            try:
                os.replace(entry["staged"], entry["path"].split("/")[-1], src_dir_fd=parent, dst_dir_fd=parent)
                os.fsync(parent)
            finally:
                os.close(parent)
        private_write(journal_fd, "committed.json", json.dumps({"entries": entries, "committed": True}).encode())
        os.replace("committed.json", "pending.json", src_dir_fd=journal_fd, dst_dir_fd=journal_fd)
        os.fsync(journal_fd)
        recover(root_fd, journal_fd)
        pending = False
    except BaseException:
        if pending:
            try:
                recover(root_fd, journal_fd)
            finally:
                pending = False
        raise
    finally:
        for entry in ([] if pending else entries):
            try:
                parent = directory(root_fd, entry["path"].split("/")[:-1])
            except (FileNotFoundError, NotADirectoryError):
                continue
            try:
                for name in (entry["staged"], entry["backup"]):
                    try:
                        os.unlink(name, dir_fd=parent)
                    except FileNotFoundError:
                        pass
            finally:
                os.close(parent)
        os.close(journal_fd)

def main(home=None):
    root_fd = None
    lock_fd = None
    try:
        request = json.load(sys.stdin)
        paths = request["paths"]
        if not isinstance(paths, list) or not paths or len(paths) > 16:
            raise ValueError("model_config_invalid")
        for p in paths:
            if not isinstance(p, str) or p.startswith("/") or "\\" in p or "\x00" in p or any(x in ("", ".", "..") for x in p.split("/")):
                raise ValueError("model_config_invalid")
        root_fd = os.open(home or pwd.getpwuid(os.getuid()).pw_dir,
                          os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        journal_fd = directory(root_fd, [".multica", "runtime-model-config"], True)
        try:
            lock_fd = os.open("lock", os.O_CREAT | os.O_WRONLY | os.O_NOFOLLOW, 0o600, dir_fd=journal_fd)
            fcntl.flock(lock_fd, fcntl.LOCK_EX)
            recover(root_fd, journal_fd)
        finally:
            os.close(journal_fd)
        current = snapshot(root_fd, paths)
        if request["action"] == "save":
            save(root_fd, request, current)
            current = snapshot(root_fd, paths)
        elif request["action"] != "read":
            raise ValueError("model_config_invalid")
        print(json.dumps(current))
    except Exception as exc:
        codes = ("model_config_conflict", "model_config_exists", "model_config_invalid")
        code = str(exc) if isinstance(exc, ValueError) and str(exc) in codes else "credential_transfer_failed"
        print(json.dumps({"error_code": code}))
    finally:
        if lock_fd is not None:
            os.close(lock_fd)
        if root_fd is not None:
            os.close(root_fd)


if __name__ == "__main__":
    main()
