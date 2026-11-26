"""Independent check: closing the inheritance widget must leave no handler of the widget connected
to the viewer's selection events or to the selected layer's name event.

Runs with the standard library only. qtpy, numpy and pint are replaced by small stand-ins, and the
napari viewer by a fake with recording emitters. The two project modules are loaded from src/
directly so the Qt-heavy package __init__ files are not executed.
"""
import importlib.util
import sys
import types
from pathlib import Path

SRC = Path(__file__).resolve().parent / "src" / "napari_metadata"


class _Namespace:
    def __getattr__(self, name):
        return _Namespace()


class _Meta(type):
    def __getattr__(cls, name):
        return _Namespace()


class _Anything(metaclass=_Meta):
    """Permissive Qt stand-in: any attribute is a no-op callable or a nested stand-in."""

    def __init__(self, *args, **kwargs):
        pass

    def __getattr__(self, name):
        return _Anything()

    def __call__(self, *args, **kwargs):
        return None

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False


class QWidget(_Anything):
    def closeEvent(self, event):
        return None


def _install_stubs():
    qtpy = types.ModuleType("qtpy")
    core = types.ModuleType("qtpy.QtCore")
    widgets = types.ModuleType("qtpy.QtWidgets")
    core.QSignalBlocker = _Anything
    core.Qt = _Namespace()
    for name in ("QComboBox", "QLabel", "QPushButton", "QSizePolicy", "QVBoxLayout"):
        setattr(widgets, name, _Anything)
    widgets.QWidget = QWidget
    qtpy.QtCore, qtpy.QtWidgets = core, widgets
    sys.modules.update({"qtpy": qtpy, "qtpy.QtCore": core, "qtpy.QtWidgets": widgets,
                        "numpy": types.ModuleType("numpy"), "pint": types.ModuleType("pint")})
    pkg = types.ModuleType("napari_metadata")
    pkg.__path__ = [str(SRC)]
    sub = types.ModuleType("napari_metadata.widgets")
    sub.__path__ = [str(SRC / "widgets")]
    sys.modules.update({"napari_metadata": pkg, "napari_metadata.widgets": sub})


def _load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


class Emitter:
    def __init__(self):
        self.callbacks = []

    def connect(self, cb):
        self.callbacks.append(cb)

    def disconnect(self, cb):
        self.callbacks.remove(cb)

    def __call__(self, *args):
        for cb in list(self.callbacks):
            cb()


class Events:
    def __init__(self, *names):
        for n in names:
            setattr(self, n, Emitter())


class Layer:
    def __init__(self, name):
        self.name = name
        self.ndim = 2
        self.events = Events("name")


class Selection:
    def __init__(self):
        self.events = Events("active")
        self._active = None

    @property
    def active(self):
        return self._active

    @active.setter
    def active(self, layer):
        self._active = layer
        self.events.active()


class LayerList(list):
    def __init__(self, layers):
        super().__init__(layers)
        self.events = Events("inserted", "removed", "changed")
        self.selection = Selection()


class Viewer:
    def __init__(self, layers):
        self.layers = LayerList(layers)


def main():
    _install_stubs()
    _load("napari_metadata.layer_utils", SRC / "layer_utils.py")
    inheritance = _load("napari_metadata.widgets._inheritance", SRC / "widgets" / "_inheritance.py")
    first, second = Layer("first"), Layer("second")
    viewer = Viewer([first, second])
    widget = inheritance.InheritanceWidget(viewer)
    viewer.layers.selection.active = first
    if not first.events.name.callbacks:
        print("SETUP: selecting a layer did not subscribe the widget to its name event")
        return 2
    widget.closeEvent(None)
    stale = []
    for label, emitter in (("selection.active", viewer.layers.selection.events.active),
                           ("layer.name", first.events.name),
                           ("layers.inserted", viewer.layers.events.inserted),
                           ("layers.removed", viewer.layers.events.removed),
                           ("layers.changed", viewer.layers.events.changed)):
        owned = [cb for cb in emitter.callbacks if getattr(cb, "__self__", None) is widget]
        if owned:
            stale.append(f"{label}: {', '.join(cb.__name__ for cb in owned)}")
    if stale:
        print("STALE SUBSCRIPTIONS AFTER CLOSE: " + "; ".join(stale))
        return 1
    print("closed widget holds no subscriptions")
    return 0


if __name__ == "__main__":
    sys.exit(main())
