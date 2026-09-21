import os
import io
from typing import List, Dict, Any, Optional
from googleapiclient.discovery import build
from googleapiclient.http import MediaFileUpload, MediaIoBaseDownload
from google.oauth2.credentials import Credentials

class DriveClient:
    """Wrapper around Google Drive API v3 for file and directory operations."""
    def __init__(self, creds: Credentials):
        self.service = build('drive', 'v3', credentials=creds, cache_discovery=False)

    def list_folder_contents(self, folder_id: str = 'root') -> List[Dict[str, Any]]:
        """
        Lists all files and folders recursively inside a given Google Drive folder.
        Returns list of metadata dicts (id, name, mimeType, md5Checksum, modifiedTime, size, parents).
        """
        items = []
        page_token = None
        query = f"'{folder_id}' in parents and trashed = false"

        while True:
            response = self.service.files().list(
                q=query,
                spaces='drive',
                fields='nextPageToken, files(id, name, mimeType, md5Checksum, modifiedTime, size, parents)',
                pageToken=page_token,
                pageSize=100
            ).execute()

            for item in response.get('files', []):
                items.append(item)

            page_token = response.get('nextPageToken', None)
            if not page_token:
                break
        return items

    def create_folder(self, name: str, parent_id: str = 'root') -> str:
        """Creates a folder in Google Drive and returns its ID."""
        file_metadata = {
            'name': name,
            'mimeType': 'application/vnd.google-apps.folder',
            'parents': [parent_id]
        }
        folder = self.service.files().create(body=file_metadata, fields='id').execute()
        return folder.get('id')

    def upload_file(self, local_path: str, parent_id: str = 'root', remote_name: Optional[str] = None) -> Dict[str, Any]:
        """
        Uploads a new file to Google Drive using resumable chunk upload.
        Returns file metadata including id and md5Checksum.
        """
        name = remote_name or os.path.basename(local_path)
        file_metadata = {
            'name': name,
            'parents': [parent_id]
        }
        media = MediaFileUpload(local_path, resumable=True)
        file = self.service.files().create(
            body=file_metadata,
            media_body=media,
            fields='id, name, md5Checksum, modifiedTime, size'
        ).execute()
        return file

    def update_file_content(self, file_id: str, local_path: str) -> Dict[str, Any]:
        """Updates content of an existing Google Drive file."""
        media = MediaFileUpload(local_path, resumable=True)
        file = self.service.files().update(
            fileId=file_id,
            media_body=media,
            fields='id, name, md5Checksum, modifiedTime, size'
        ).execute()
        return file

    def download_file(self, file_id: str, local_dest_path: str):
        """
        Downloads a file from Google Drive to local disk atomically using a .tmp file.
        """
        tmp_dest_path = local_dest_path + ".tmp"
        os.makedirs(os.path.dirname(os.path.abspath(local_dest_path)), exist_ok=True)

        request = self.service.files().get_media(fileId=file_id)
        with io.FileIO(tmp_dest_path, 'wb') as fh:
            downloader = MediaIoBaseDownload(fh, request, chunksize=1024 * 1024)
            done = False
            while not done:
                _, done = downloader.next_chunk()

        if os.path.exists(local_dest_path):
            os.remove(local_dest_path)
        os.rename(tmp_dest_path, local_dest_path)

    def trash_file(self, file_id: str):
        """Moves file to Google Drive trash instead of permanently deleting."""
        self.service.files().update(fileId=file_id, body={'trashed': True}).execute()
