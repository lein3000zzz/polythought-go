"""Polythought from Python: its data and Polymarket's as tables, your datasets, and serving
your models to its workflows.

    from polythought import Client, serve

    pt = Client()  # POLYTHOUGHT_URL and POLYTHOUGHT_TOKEN
    pt.datasets.frame("forecasts")
    serve(predict)  # POLYTHOUGHT_WORKER_KEY
"""

from ._version import __version__
from .client import Client
from .errors import PolythoughtError
from .frames import frame
from .serve import Predict, serve

__all__ = ["Client", "PolythoughtError", "Predict", "__version__", "frame", "serve"]
