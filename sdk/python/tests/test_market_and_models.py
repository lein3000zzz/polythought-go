from __future__ import annotations

import pandas as pd

from polythought import Client

from .conftest import Fake, ok


def test_markets_books_and_prices(fake: Fake, client: Client) -> None:
    fake.on(
        "GET",
        "/market-data/search",
        ok({"markets": [{"id": "m-1", "question": "NYC high above 50?"}]}),
    )
    fake.on("GET", "/market-data/books", ok({"books": []}))
    fake.on(
        "GET",
        "/market-data/history/t-1",
        ok(
            {
                "tokenId": "t-1",
                "window": "1d",
                "points": [{"at": "2026-11-24T12:00:00Z", "price": "0.53"}],
            }
        ),
    )

    assert client.market.search("NYC", limit=5)[0]["id"] == "m-1"
    assert fake.calls[0].params == {"query": "NYC", "closed": "false", "limit": "5"}

    client.market.books(["t-1", "t-2"], depth=3)
    assert fake.sent("GET", "/market-data/books")[0].params == {"tokens": "t-1,t-2", "depth": "3"}

    table = client.market.history_frame("t-1", window="1d")
    assert table["price"].iloc[0] == 0.53
    assert table.index[0] == pd.Timestamp("2026-11-24T12:00:00Z")


def test_asks_a_model_waiting_long_enough(fake: Fake, client: Client) -> None:
    fake.on(
        "POST",
        "/models/temps/ask",
        ok({"answers": [{"high": 51.25}], "version": "1.2.0", "worker": "gpu-1"}),
    )

    got = client.models.ask("temps", [{"city": "NYC"}], schema={"type": "object"}, wait=60)

    assert got["answers"] == [{"high": 51.25}]
    assert fake.calls[0].body == {
        "items": [{"city": "NYC"}],
        "waitSeconds": 60,
        "schema": {"type": "object"},
    }
