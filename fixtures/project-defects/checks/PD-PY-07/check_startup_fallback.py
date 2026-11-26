"""Independent check: start-up when the resource catalogue cannot be fetched.

The main window is built with stand-ins for Tk, Pillow, psutil, requests and the sibling modules that
need a display or the network. The catalogue fetch raises. The value main() passes to the resource tree
is captured and fed to the real tree walker of the ui package, which is what the real tree builder does
with it first. Standard library only; nothing touches the network or the user's config directory.
"""
import importlib.util
import sys
import types
from pathlib import Path
from unittest import mock

PKG = Path(__file__).resolve().parent / "src" / "tchmaterial_parser"


class Stub(types.ModuleType):
    def __getattr__(self, name):
        if name.startswith("__"):
            raise AttributeError(name)
        value = mock.MagicMock(name=f"{self.__name__}.{name}")
        setattr(self, name, value)
        return value


def stub(name, **attrs):
    module = Stub(name)
    for key, value in attrs.items():
        setattr(module, key, value)
    sys.modules[name] = module
    parent, _, child = name.rpartition(".")
    if parent in sys.modules:
        setattr(sys.modules[parent], child, module)
    return module


def package(name, path, **attrs):
    module = types.ModuleType(name)
    module.__path__ = [str(path)]
    module.__dict__.update(attrs)
    sys.modules[name] = module
    return module


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    parent, _, child = name.rpartition(".")
    setattr(sys.modules[parent], child, module)
    return module


class CatalogueUnavailable(Exception):
    pass


class Reached(Exception):
    pass


def main():
    root = mock.MagicMock(name="root")
    root.winfo_fpixels.return_value = 96.0
    root.winfo_screenwidth.return_value = 1920
    root.winfo_screenheight.return_value = 1080
    tk = stub("tkinter", Tk=mock.MagicMock(return_value=root))
    stub("tkinter.ttk")
    stub("tkinter.messagebox")
    stub("tkinter.font")
    tk.ttk, tk.messagebox, tk.font = sys.modules["tkinter.ttk"], sys.modules["tkinter.messagebox"], sys.modules["tkinter.font"]
    for name in ("psutil", "requests", "PIL", "PIL.Image", "PIL.ImageTk", "PIL.ImageDraw", "PIL.ImageOps"):
        stub(name)

    package("tchmaterial_parser", PKG, __version__="0.0-check")
    helper = mock.MagicMock()
    helper.return_value.fetch_resource_list.side_effect = CatalogueUnavailable("catalogue fetch failed")
    stub("tchmaterial_parser.catalog", ResourceHelper=helper)
    stub("tchmaterial_parser.config", load_config=lambda: {}, load_access_token=lambda config: None,
         save_config=lambda **kw: None)
    stub("tchmaterial_parser.images")
    stub("tchmaterial_parser.network")
    stub("tchmaterial_parser.platform_utils", os_name="Linux", print_error=lambda error: None,
         resource_path=lambda *parts: "/nonexistent", ctypes=None, win32api=None, win32con=None, win32gui=None,
         win32print=None)
    package("tchmaterial_parser.ui", PKG / "ui")
    stub("tchmaterial_parser.ui.runtime", ui_scale=1.0, app_closing=False, bind_root=lambda window: None,
         scaled=lambda size: round(size), thread_it=lambda *a, **k: None, ui_call=lambda *a, **k: None)
    for name in ("download_panel", "theme", "about_window", "token_window", "widgets"):
        stub("tchmaterial_parser.ui." + name)

    tree = load("tchmaterial_parser.ui.resource_tree", PKG / "ui" / "resource_tree.py")
    app = load("tchmaterial_parser.app", PKG / "app.py")

    captured = []

    def recorder(pane, resource_list, url_text):
        captured.append(resource_list)
        raise Reached

    app.build_resource_tree = recorder
    try:
        app.main()
    except Reached:
        pass
    except Exception as error:  # a stand-in did not fit; not a verdict on the code under test
        print(f"CHECK SETUP ERROR before the resource tree was built: {type(error).__name__}: {error}")
        return 3
    if not captured:
        print("CHECK SETUP ERROR: main() returned without building the resource tree")
        return 3
    value = captured[0]
    if not sys.modules["tkinter.messagebox"].showwarning.called:
        print("CHECK SETUP ERROR: the catalogue failure path was not taken")
        return 3
    try:
        leaves = list(tree.iter_leaf_resources(value))
    except Exception as error:
        print(f"RESOURCE TREE CANNOT USE THE FALLBACK: {type(value).__name__} -> {type(error).__name__}: {error}")
        return 1
    print(f"fallback catalogue accepted by the tree walker ({len(leaves)} entries)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
