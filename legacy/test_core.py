import unittest
import os
import tempfile
import shutil
from hasher import compute_md5
from db import StateDB

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

if __name__ == "__main__":
    unittest.main()
