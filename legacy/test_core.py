import unittest
import os
import tempfile
import shutil
from hasher import compute_md5
from db import StateDB
from syncer import SyncEngine

class TestSyncCore(unittest.TestCase):
    def setUp(self):
        self.test_dir = tempfile.mkdtemp()
        self.db_path = os.path.join(self.test_dir, "test_state.db")
        self.db = StateDB(self.db_path)

    def tearDown(self):
        shutil.rmtree(self.test_dir)

    def test_hasher_md5(self):
        file_path = os.path.join(self.test_dir, "sample.txt")
        with open(file_path, "wb") as f:
            f.write(b"Hello Google Drive Sync!")
        expected_md5 = "96844ad70852ebc9318620e79375a7fa"
        actual_md5 = compute_md5(file_path)
        self.assertEqual(actual_md5, expected_md5)

    def test_db_operations(self):
        self.db.upsert_file("docs/note.txt", "drive_id_123", "dummy_md5", 1000.0, 50, False, 1005.0)
        entry = self.db.get_file("docs/note.txt")
        self.assertIsNotNone(entry)
        self.assertEqual(entry["file_id"], "drive_id_123")
        self.assertEqual(entry["md5"], "dummy_md5")
        
        all_files = self.db.get_all_files()
        self.assertIn("docs/note.txt", all_files)
        
        self.db.remove_file("docs/note.txt")
        self.assertIsNone(self.db.get_file("docs/note.txt"))

    def test_sync_refuses_missing_local_root_before_remote_access(self):
        missing_root = os.path.join(self.test_dir, "missing")

        class RemoteMustNotBeCalled:
            def list_folder_contents(self, _folder_id):
                raise AssertionError("remote tree must not be fetched when local root is missing")

        engine = SyncEngine(missing_root, "root-id", RemoteMustNotBeCalled(), self.db)
        with self.assertRaises(FileNotFoundError):
            engine.sync()
        self.assertFalse(os.path.exists(missing_root))

    def test_scan_refuses_symlink_without_returning_partial_snapshot(self):
        root = os.path.join(self.test_dir, "sync")
        os.mkdir(root)
        outside = os.path.join(self.test_dir, "outside.txt")
        with open(outside, "wb") as f:
            f.write(b"outside")
        try:
            os.symlink(outside, os.path.join(root, "linked.txt"))
        except (OSError, NotImplementedError) as exc:
            self.skipTest(f"symlink creation unavailable: {exc}")

        engine = SyncEngine(root, "root-id", None, self.db)
        with self.assertRaises(OSError):
            engine.scan_local_files()

if __name__ == "__main__":
    unittest.main()
