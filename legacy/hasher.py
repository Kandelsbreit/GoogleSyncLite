import hashlib
import os

def compute_md5(file_path: str, block_size: int = 1024 * 1024) -> str:
    """Computes the MD5 hash of a file in streaming chunks (memory efficient)."""
    if not os.path.isfile(file_path):
        return ""
    hasher = hashlib.md5()
    with open(file_path, "rb") as f:
        for chunk in iter(lambda: f.read(block_size), b""):
            hasher.update(chunk)
    return hasher.hexdigest()
