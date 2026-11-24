"""The client: Polythought's API with an API token."""

from __future__ import annotations

import os
from collections.abc import Iterator, Mapping
from typing import TYPE_CHECKING, Any, cast

import httpx

from ._json import dumps
from ._version import __version__
from .errors import PolythoughtError
from .types import Execution, Holding, HoldingList, LoggedOrder, Workflow

if TYPE_CHECKING:
    import pandas as pd

    from .datasets import Datasets
    from .market import Market
    from .models import Models


def base_url(url: str | None) -> str:
    """The API's base: url, or POLYTHOUGHT_URL, and /api/v1."""
    url = url or os.environ.get("POLYTHOUGHT_URL", "")
    if not url:
        raise ValueError("no Polythought URL: pass url=, or set POLYTHOUGHT_URL")

    return url.rstrip("/") + "/api/v1"


def fail(resp: httpx.Response) -> PolythoughtError:
    """The error an answer that is no success says."""
    code, message = "error", resp.text[:500]

    try:
        body = resp.json()
    except ValueError:
        body = None

    if isinstance(body, dict) and isinstance(body.get("error"), dict):
        code = str(body["error"].get("code", code))
        message = str(body["error"].get("message", message))

    return PolythoughtError(resp.status_code, code, message)


class Client:
    """Polythought's API, with an API token: what you may read, and the datasets and models
    your token may write.

    The URL and the token come from POLYTHOUGHT_URL and POLYTHOUGHT_TOKEN unless given::

        pt = Client("https://polythought.example.com", token="pt_…")
        pt.datasets.frame("forecasts")
    """

    def __init__(
        self,
        url: str | None = None,
        token: str | None = None,
        *,
        timeout: float = 60.0,
        transport: httpx.BaseTransport | None = None,
    ) -> None:
        token = token or os.environ.get("POLYTHOUGHT_TOKEN", "")
        if not token:
            raise ValueError("no API token: pass token=, or set POLYTHOUGHT_TOKEN")

        self._http = httpx.Client(
            base_url=base_url(url),
            headers={
                "Authorization": f"Bearer {token}",
                "User-Agent": f"polythought-python/{__version__}",
            },
            timeout=timeout,
            transport=transport,
        )

        from .datasets import Datasets
        from .market import Market
        from .models import Models

        self.datasets: Datasets = Datasets(self)
        self.models: Models = Models(self)
        self.market: Market = Market(self)

    def __enter__(self) -> Client:
        return self

    def __exit__(self, *_: object) -> None:
        self.close()

    def close(self) -> None:
        self._http.close()

    def request(
        self,
        method: str,
        path: str,
        *,
        params: Mapping[str, Any] | None = None,
        body: Any = None,
        timeout: float | None = None,
    ) -> httpx.Response:
        """Calls the API, raising PolythoughtError for an answer that is no success."""
        kwargs: dict[str, Any] = {}
        if params is not None:
            kwargs["params"] = {k: v for k, v in params.items() if v is not None}
        if body is not None:
            kwargs["content"] = dumps(body).encode()
            kwargs["headers"] = {"Content-Type": "application/json"}
        if timeout is not None:
            kwargs["timeout"] = timeout

        resp = self._http.request(method, path, **kwargs)
        if resp.is_error:
            raise fail(resp)

        return resp

    def get(self, path: str, **params: Any) -> Any:
        return self.request("GET", path, params=params).json()

    def _pages(self, path: str, field: str, limit: int, params: Mapping[str, Any]) -> Iterator[Any]:
        """Each of a cursor-paged list's entries, up to limit."""
        cursor: str | None = None
        left = limit

        while left > 0:
            page = self.get(path, **params, limit=min(left, 100), cursor=cursor)
            for entry in page.get(field, []):
                yield entry
                left -= 1
                if left == 0:
                    return

            cursor = page.get("nextCursor")
            if not cursor:
                return

    def workflows(
        self, *, active: bool | None = None, query: str | None = None, limit: int = 1000
    ) -> list[Workflow]:
        """Your workflows, the pinned first, then the newest."""
        pages = self._pages("/workflows", "workflows", limit, {"active": active, "q": query})
        return [cast(Workflow, w) for w in pages]

    def executions(self, workflow: str, *, limit: int = 100) -> list[Execution]:
        """A workflow's runs, the newest first."""
        pages = self._pages(f"/workflows/{workflow}/executions", "executions", limit, {})
        return [cast(Execution, e) for e in pages]

    def orders(
        self,
        *,
        workflow: str | None = None,
        wallet: str | None = None,
        status: str | None = None,
        limit: int = 1000,
    ) -> list[LoggedOrder]:
        """Your workflows' orders, the newest first: one workflow's, one wallet's, or those
        of a status, like filled."""
        pages = self._pages(
            "/orders", "orders", limit, {"workflow": workflow, "wallet": wallet, "status": status}
        )
        return [cast(LoggedOrder, o) for o in pages]

    def holdings(self, workflow: str) -> list[Holding]:
        """What a workflow holds now, in its mode."""
        return cast(HoldingList, self.get(f"/workflows/{workflow}/holdings"))["holdings"]

    def orders_frame(self, **kwargs: Any) -> pd.DataFrame:
        """orders as a table: a row an order."""
        from .frames import frame

        return frame(self.orders(**kwargs))
