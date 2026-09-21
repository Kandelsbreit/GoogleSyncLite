import os
import time
from datetime import datetime
from typing import Dict, Any, List, Tuple
from db import StateDB
from hasher import compute_md5
from drive_api import DriveClient

class SyncEngine:
    """
    SyncEngine coordinates two-way synchronization between a local directory
    and a Google Drive folder with hash-based validation and conflict preservation.
    """
    def __init__(self, local_root: str, remote_root_id: str, drive_client: DriveClient, db: StateDB):
        self.local_root = os.path.abspath(local_root)
        self.remote_root_id = remote_root_id
        self.drive = drive_client
        self.db = db

    def scan_local_files(self) -> Dict[str, Dict[str, Any]]:
        """Walks local folder, collects relative paths, size, and mtime."""
        local_files = {}
        if not os.path.exists(self.local_root):
            os.makedirs(self.local_root, exist_ok=True)

        for root, dirs, files in os.walk(self.local_root):
            # Ignore temporary sync files and hidden system files
            dirs[:] = [d for d in dirs if not d.startswith('.') and d != '__pycache__']
            for f in files:
                if f.endswith('.tmp') or f.startswith('.'):
                    continue
                full_path = os.path.join(root, f)
                rel_path = os.path.relpath(full_path, self.local_root).replace('\\', '/')
                try:
                    stat = os.stat(full_path)
                    local_files[rel_path] = {
                        'full_path': full_path,
                        'size': stat.st_size,
                        'mtime': stat.st_mtime
                    }
                except OSError:
                    continue
        return local_files

    def fetch_remote_tree(self) -> Dict[str, Dict[str, Any]]:
        """
        Recursively maps remote folder structure into a dictionary of {relative_path: metadata}.
        Also maps folder paths to folder_ids.
        """
        remote_files = {}
        folders_map = {"": self.remote_root_id}

        def traverse_folder(current_folder_id: str, current_prefix: str):
            items = self.drive.list_folder_contents(current_folder_id)
            for item in items:
                name = item['name']
                rel_path = f"{current_prefix}/{name}" if current_prefix else name
                mime = item.get('mimeType', '')

                if mime == 'application/vnd.google-apps.folder':
                    folders_map[rel_path] = item['id']
                    traverse_folder(item['id'], rel_path)
                else:
                    remote_files[rel_path] = {
                        'id': item['id'],
                        'name': name,
                        'size': int(item.get('size', 0)),
                        'md5': item.get('md5Checksum', ''),
                        'modifiedTime': item.get('modifiedTime'),
                        'parent_id': current_folder_id
                    }

        traverse_folder(self.remote_root_id, "")
        self.remote_folders = folders_map
        return remote_files

    def ensure_remote_folder_path(self, rel_dir: str) -> str:
        """Ensures that remote folder hierarchy exists for a given relative folder path."""
        if not rel_dir or rel_dir == ".":
            return self.remote_root_id

        parts = rel_dir.replace('\\', '/').strip('/').split('/')
        accumulated_path = ""
        parent_id = self.remote_root_id

        for part in parts:
            accumulated_path = f"{accumulated_path}/{part}" if accumulated_path else part
            if accumulated_path in self.remote_folders:
                parent_id = self.remote_folders[accumulated_path]
            else:
                print(f"[*] Creating remote directory: {accumulated_path}")
                new_id = self.drive.create_folder(part, parent_id)
                self.remote_folders[accumulated_path] = new_id
                parent_id = new_id

        return parent_id

    def sync(self, dry_run: bool = False):
        """Executes full sync pass with verification."""
        print("[*] Scanning local filesystem...")
        local_files = self.scan_local_files()
        print(f"    Found {len(local_files)} local files.")

        print("[*] Fetching Google Drive tree...")
        remote_files = self.fetch_remote_tree()
        print(f"    Found {len(remote_files)} remote files.")

        stored_state = self.db.get_all_files()

        all_rel_paths = set(local_files.keys()) | set(remote_files.keys()) | set(stored_state.keys())

        uploaded_count = 0
        downloaded_count = 0
        verified_count = 0
        conflicts_count = 0

        for path in sorted(all_rel_paths):
            loc = local_files.get(path)
            rem = remote_files.get(path)
            prev = stored_state.get(path)

            # Case 1: File is both local and remote
            if loc and rem:
                # Fast check: size match, check MD5
                loc_md5 = compute_md5(loc['full_path'])
                rem_md5 = rem['md5']

                if loc_md5 and rem_md5 and loc_md5 == rem_md5:
                    # In sync, update DB state
                    self.db.upsert_file(path, rem['id'], loc_md5, loc['mtime'], loc['size'], False, time.time())
                    verified_count += 1
                    continue

                # Hashes differ: check who modified it since last sync
                if prev:
                    prev_md5 = prev.get('md5')
                    local_changed = (loc_md5 != prev_md5)
                    remote_changed = (rem_md5 != prev_md5)

                    if local_changed and not remote_changed:
                        print(f"[↑] Uploading local change: {path}")
                        if not dry_run:
                            res = self.drive.update_file_content(rem['id'], loc['full_path'])
                            new_md5 = res.get('md5Checksum', loc_md5)
                            self.db.upsert_file(path, rem['id'], new_md5, loc['mtime'], loc['size'], False, time.time())
                        uploaded_count += 1

                    elif remote_changed and not local_changed:
                        print(f"[↓] Downloading remote change: {path}")
                        if not dry_run:
                            self.drive.download_file(rem['id'], loc['full_path'])
                            verify_hash = compute_md5(loc['full_path'])
                            if verify_hash != rem_md5:
                                raise IOError(f"MD5 mismatch after downloading {path}")
                            stat = os.stat(loc['full_path'])
                            self.db.upsert_file(path, rem['id'], rem_md5, stat.st_mtime, stat.st_size, False, time.time())
                        downloaded_count += 1

                    else:
                        # Conflict! Both changed. Preserve both safely!
                        print(f"[!] CONFLICT detected on '{path}'. Preserving both files.")
                        timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
                        base, ext = os.path.splitext(loc['full_path'])
                        conflict_path = f"{base}_conflict_{timestamp}{ext}"
                        if not dry_run:
                            # Move local to conflict file
                            os.rename(loc['full_path'], conflict_path)
                            # Download remote version to original path
                            self.drive.download_file(rem['id'], loc['full_path'])
                        conflicts_count += 1
                else:
                    # Not previously tracked, but exists in both places with different content
                    print(f"[!] Conflict/untracked mismatch for '{path}'. Backing up local copy.")
                    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
                    base, ext = os.path.splitext(loc['full_path'])
                    conflict_path = f"{base}_local_backup_{timestamp}{ext}"
                    if not dry_run:
                        os.rename(loc['full_path'], conflict_path)
                        self.drive.download_file(rem['id'], loc['full_path'])
                    conflicts_count += 1

            # Case 2: Only local exists
            elif loc and not rem:
                if prev:
                    # Was synced before, but disappeared from remote -> local should be deleted or kept?
                    # Safe approach: keep file or move to local .trash
                    print(f"[-] File '{path}' was removed from remote. Keeping local copy safely.")
                    self.db.remove_file(path)
                else:
                    # New local file
                    print(f"[↑] Uploading new local file: {path}")
                    if not dry_run:
                        rel_dir = os.path.dirname(path)
                        parent_id = self.ensure_remote_folder_path(rel_dir)
                        res = self.drive.upload_file(loc['full_path'], parent_id=parent_id, remote_name=os.path.basename(path))
                        loc_md5 = compute_md5(loc['full_path'])
                        self.db.upsert_file(path, res['id'], res.get('md5Checksum', loc_md5), loc['mtime'], loc['size'], False, time.time())
                    uploaded_count += 1

            # Case 3: Only remote exists
            elif rem and not loc:
                if prev:
                    # Was deleted locally. Remove from remote (to trash for safety)
                    print(f"[-] Local file was deleted. Moving remote to trash: {path}")
                    if not dry_run:
                        self.drive.trash_file(rem['id'])
                        self.db.remove_file(path)
                else:
                    # New remote file -> download
                    print(f"[↓] Downloading new file from Drive: {path}")
                    local_dest = os.path.join(self.local_root, path.replace('/', os.sep))
                    if not dry_run:
                        self.drive.download_file(rem['id'], local_dest)
                        verify_hash = compute_md5(local_dest)
                        if rem['md5'] and verify_hash != rem['md5']:
                            raise IOError(f"Integrity check failed for {path} (expected {rem['md5']}, got {verify_hash})")
                        stat = os.stat(local_dest)
                        self.db.upsert_file(path, rem['id'], rem['md5'], stat.st_mtime, stat.st_size, False, time.time())
                    downloaded_count += 1

            # Case 4: File was in DB, but neither local nor remote exists anymore
            elif prev and not loc and not rem:
                self.db.remove_file(path)

        print("\n[+] Sync completed successfully:")
        print(f"    - Verified up-to-date: {verified_count}")
        print(f"    - Uploaded: {uploaded_count}")
        print(f"    - Downloaded: {downloaded_count}")
        print(f"    - Conflicts preserved: {conflicts_count}")

    def verify_integrity(self) -> Tuple[int, int, List[str]]:
        """
        Audit command: checks every local file against its Google Drive MD5 checksum.
        Guarantees that files on disk match the cloud byte-for-byte.
        """
        print("[*] Running full integrity verification (MD5 self-check)...")
        local_files = self.scan_local_files()
        remote_files = self.fetch_remote_tree()

        matched = 0
        mismatched = 0
        errors = []

        for path, loc in local_files.items():
            rem = remote_files.get(path)
            if not rem:
                errors.append(f"Missing in cloud: {path}")
                mismatched += 1
                continue

            loc_md5 = compute_md5(loc['full_path'])
            rem_md5 = rem.get('md5')

            if not rem_md5:
                # E.g. Google Docs / native types don't have md5Checksum
                continue

            if loc_md5 == rem_md5:
                matched += 1
            else:
                mismatched += 1
                errors.append(f"Checksum mismatch: {path} (local: {loc_md5}, cloud: {rem_md5})")

        return matched, mismatched, errors
