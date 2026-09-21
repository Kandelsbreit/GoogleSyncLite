import os
import json
from google.auth.transport.requests import Request
from google.oauth2.credentials import Credentials
from google_auth_oauthlib.flow import InstalledAppFlow

# Google Drive full file access scope for syncing user folder
SCOPES = ['https://www.googleapis.com/auth/drive']

CREDENTIALS_FILE = 'credentials.json'
TOKEN_FILE = 'token.json'

def get_credentials() -> Credentials:
    """
    Handles browser-based OAuth 2.0 flow.
    Reads token.json if present, refreshes if expired,
    or launches local browser flow to authenticate.
    """
    creds = None
    if os.path.exists(TOKEN_FILE):
        try:
            creds = Credentials.from_authorized_user_file(TOKEN_FILE, SCOPES)
        except Exception as e:
            print(f"[!] Error loading existing token: {e}")
            creds = None

    if not creds or not creds.valid:
        if creds and creds.expired and creds.refresh_token:
            try:
                print("[*] Refreshing expired authentication token...")
                creds.refresh(Request())
            except Exception as e:
                print(f"[!] Token refresh failed ({e}). Need to re-authenticate.")
                creds = None

        if not creds:
            if not os.path.exists(CREDENTIALS_FILE):
                raise FileNotFoundError(
                    f"Configuration file '{CREDENTIALS_FILE}' not found!\n"
                    "Please download OAuth Client ID JSON from Google Cloud Console "
                    f"and place it as '{os.path.abspath(CREDENTIALS_FILE)}'."
                )

            print("[*] Opening system browser for Google Account authentication...")
            flow = InstalledAppFlow.from_client_secrets_file(CREDENTIALS_FILE, SCOPES)
            # Runs local web server to catch auth redirect callback
            creds = flow.run_local_server(port=8085, prompt='consent', access_type='offline')

        with open(TOKEN_FILE, 'w', encoding='utf-8') as token_out:
            token_out.write(creds.to_json())
        print(f"[+] Authentication successful. Token saved to '{TOKEN_FILE}'.")

    return creds

if __name__ == "__main__":
    try:
        creds = get_credentials()
        print("[+] Authorized successfully!")
    except Exception as err:
        print(f"[-] Auth error: {err}")
