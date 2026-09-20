import pystray
from pystray import MenuItem as item
from icon_generator import create_tray_icon_image

class TrayApp:
    """Manages the Windows System Tray icon and background interactions."""
    def __init__(self, on_open_gui, on_sync_now, on_exit):
        self.on_open_gui = on_open_gui
        self.on_sync_now = on_sync_now
        self.on_exit = on_exit
        self.icon = None

    def _create_menu(self):
        return pystray.Menu(
            item('Открыть настройки', self._handle_open, default=True),
            item('Синхронизировать сейчас', self._handle_sync),
            pystray.Menu.SEPARATOR,
            item('Выход', self._handle_exit)
        )

    def _handle_open(self, icon, item):
        if self.on_open_gui:
            self.on_open_gui()

    def _handle_sync(self, icon, item):
        if self.on_sync_now:
            self.on_sync_now()

    def _handle_exit(self, icon, item):
        if self.icon:
            self.icon.stop()
        if self.on_exit:
            self.on_exit()

    def run(self):
        """Runs the tray icon loop (should be run in a separate thread)."""
        image = create_tray_icon_image()
        self.icon = pystray.Icon("GoogleSyncLite", image, "Google Sync Lite", self._create_menu())
        self.icon.run()

    def update_icon_state(self, syncing: bool):
        if self.icon:
            self.icon.icon = create_tray_icon_image(syncing=syncing)
            self.icon.title = "Google Sync Lite (Синхронизация...)" if syncing else "Google Sync Lite (Готово)"

    def notify(self, title: str, message: str):
        if self.icon:
            try:
                self.icon.notify(message, title)
            except Exception:
                pass

    def stop(self):
        if self.icon:
            self.icon.stop()
