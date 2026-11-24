"""Polymarket's data, through Polythought's cache: markets, books, prices and trades."""

from __future__ import annotations

from collections.abc import Sequence
from typing import TYPE_CHECKING, cast
from urllib.parse import quote

from .types import (
    AddressPosition,
    AddressPositionList,
    MarketList,
    MarketTrade,
    MarketTradeList,
    OrderBook,
    OrderBookList,
    PriceHistory,
)
from .types import (
    Market as MarketInfo,
)

if TYPE_CHECKING:
    import pandas as pd

    from .client import Client


class Market:
    """Polymarket's data, read with any token."""

    def __init__(self, client: Client) -> None:
        self._client = client

    def search(self, query: str, *, closed: bool = False, limit: int = 20) -> list[MarketInfo]:
        """Markets whose question or event holds query, those still trading unless closed."""
        got = self._client.get("/market-data/search", query=query, closed=closed, limit=limit)
        return cast(MarketList, got)["markets"]

    def market(self, market: str) -> MarketInfo:
        """A market, by its ID, slug or link."""
        return cast(MarketInfo, self._client.get(f"/market-data/markets/{quote(market, safe='')}"))

    def trades(self, market: str, *, limit: int = 100) -> list[MarketTrade]:
        """A market's latest trades, the newest first."""
        got = self._client.get(f"/market-data/markets/{quote(market, safe='')}/trades", limit=limit)
        return cast(MarketTradeList, got)["trades"]

    def books(self, tokens: Sequence[str], *, depth: int | None = None) -> list[OrderBook]:
        """The order books of outcome tokens, each to depth levels a side."""
        got = self._client.get("/market-data/books", tokens=",".join(tokens), depth=depth)
        return cast(OrderBookList, got)["books"]

    def history(self, token: str, *, window: str = "1d", limit: int | None = None) -> PriceHistory:
        """An outcome token's prices over a window, like 1h, 1d, 1w or max."""
        got = self._client.get(
            f"/market-data/history/{quote(token, safe='')}", window=window, limit=limit
        )
        return cast(PriceHistory, got)

    def history_frame(
        self, token: str, *, window: str = "1d", limit: int | None = None
    ) -> pd.DataFrame:
        """history as a table: a time index, and its price as a number."""
        from .frames import pandas

        pd_ = pandas()
        points = self.history(token, window=window, limit=limit)["points"]
        table = pd_.DataFrame.from_records(points, columns=["at", "price"])
        table["at"] = pd_.to_datetime(table["at"], utc=True)
        table["price"] = table["price"].astype(float)

        return cast("pd.DataFrame", table.set_index("at"))

    def positions(self, address: str, *, limit: int = 100) -> list[AddressPosition]:
        """A trader's open positions, by their address."""
        got = self._client.get(
            f"/market-data/traders/{quote(address, safe='')}/positions", limit=limit
        )
        return cast(AddressPositionList, got)["positions"]
