import os
import sys
import winreg

APP_NAME = "GoogleSyncLite"
REG_PATH = r"Software\Microsoft\Windows\CurrentVersion\Run"

def get_launch_command() -> str:
    """Returns executable or python command with arguments for minimized startup."""
    if getattr(sys, 'frozen', False):
        exe_path = os.path.abspath(sys.executable)
        return f'"{exe_path}" --minimized'
    else:
        python_exe = os.path.abspath(sys.executable)
        script_path = os.path.abspath(os.path.join(os.path.dirname(__file__), "main.py"))
        # Using pythonw if available to avoid opening console window on autostart
        pythonw = os.path.join(os.path.dirname(python_exe), "pythonw.exe")
        if os.path.exists(pythonw):
            return f'"{pythonw}" "{script_path}" --minimized'
        return f'"{python_exe}" "{script_path}" --minimized'

def is_autostart_enabled() -> bool:
    """Checks if autostart registry entry exists for APP_NAME."""
    try:
        with winreg.OpenKey(winreg.HKEY_CURRENT_USER, REG_PATH, 0, winreg.KEY_READ) as key:
            winreg.QueryValueEx(key, APP_NAME)
            return True
    except (FileNotFoundError, OSError):
        return False

def set_autostart(enable: bool) -> bool:
    """Adds or removes the startup entry from Windows registry."""
    try:
        with winreg.OpenKey(winreg.HKEY_CURRENT_USER, REG_PATH, 0, winreg.KEY_SET_VALUE) as key:
            if enable:
                cmd = get_launch_command()
                winreg.SetValueEx(key, APP_NAME, 0, winreg.REG_SZ, cmd)
            else:
                try:
                    winreg.DeleteValue(key, APP_NAME)
                except FileNotFoundError:
                    pass
        return True
    except OSError as e:
        print(f"[!] Registry error: {e}")
        return False

if __name__ == "__main__":
    print(f"Autostart enabled: {is_autostart_enabled()}")
    print(f"Launch cmd: {get_launch_command()}")
