from __future__ import annotations

import httpx
import pandas as pd
import pytest

from polythought import Client, PolythoughtError
from polythought.datasets import batches

from .conftest import Fake, ok, refused

FORECASTS = {
    "name": "forecasts",
    "keyField": "city",
    "keepDays": 0,
    "keepRows": 0,
    "rows": 2,
    "bytes": 48,
}


def test_makes_one(fake: Fake, client: Client) -> None:
    fake.on("POST", "/datasets", ok(FORECASTS, 201))

    client.datasets.create("forecasts", key_field="city", keep_rows=1000)

    assert fake.calls[0].body == {
        "name": "forecasts",
        "keyField": "city",
        "keepDays": 0,
        "keepRows": 1000,
    }


def test_reads_every_row_a_page_at_a_time_and_as_a_table(fake: Fake, client: Client) -> None:
    fake.on(
        "GET",
        "/datasets/forecasts/rows",
        ok({"rows": [{"seq": 1, "at": "", "row": {"city": "NYC", "high": 51.25}}], "next": 1}),
        ok({"rows": [{"seq": 2, "at": "", "row": {"city": "LDN", "high": 9}}]}),
    )

    assert client.datasets.read("forecasts") == [
        {"city": "NYC", "high": 51.25},
        {"city": "LDN", "high": 9},
    ]
    assert [c.params["after"] for c in fake.sent("GET", "/datasets/forecasts/rows")] == ["0", "1"]

    fake.routes.clear()
    fake.on(
        "GET",
        "/datasets/forecasts/rows",
        ok({"rows": [{"seq": 1, "at": "", "row": {"city": "NYC", "high": 51.25}}]}),
    )
    table = client.datasets.frame("forecasts")
    assert list(table.columns) == ["city", "high"]
    assert table["high"][0] == 51.25


def test_a_row_by_its_key_or_none(fake: Fake, client: Client) -> None:
    fake.on("GET", "/datasets/forecasts/rows/NYC", ok({"seq": 1, "at": "", "row": {"city": "NYC"}}))
    fake.on(
        "GET", "/datasets/forecasts/rows/Mars", refused(404, "not_found", 'no row with city "Mars"')
    )
    fake.on(
        "GET", "/datasets/gone/rows/NYC", refused(404, "not_found", 'you have no dataset "gone"')
    )

    assert client.datasets.row("forecasts", "NYC") == {"city": "NYC"}
    assert client.datasets.row("forecasts", "Mars") is None

    with pytest.raises(PolythoughtError, match="no dataset"):
        client.datasets.row("gone", "NYC")


def test_appends_in_batches_a_request_may_hold(fake: Fake, client: Client) -> None:
    fake.on("POST", "/datasets/forecasts/rows", ok(FORECASTS))

    rows = [{"city": f"c-{i}", "note": "x" * 2000} for i in range(1200)]
    client.datasets.append("forecasts", rows)

    sent = fake.sent("POST", "/datasets/forecasts/rows")
    assert len(sent) > 1
    assert sum(len(c.body["rows"]) for c in sent) == 1200
    assert all(len(httpx.Request("POST", "/", json=c.body).content) < 1 << 20 for c in sent)


def test_batches_never_pass_a_thousand_rows() -> None:
    assert [len(b) for b in batches({"i": i} for i in range(2500))] == [1000, 1000, 500]


def test_replaces_at_once_numpy_and_nan_as_json(fake: Fake, client: Client) -> None:
    fake.on("POST", "/datasets/forecasts/replace", ok({"stage": "s-1", "staged": 0}, 201))
    fake.on("POST", "/datasets/forecasts/replace/s-1/rows", ok({"stage": "s-1", "staged": 2}))
    fake.on("POST", "/datasets/forecasts/replace/s-1/finish", ok(FORECASTS))

    table = pd.DataFrame({"city": ["NYC", "LDN"], "high": [51.25, float("nan")]})
    client.datasets.replace_frame("forecasts", table)

    (staged,) = fake.sent("POST", "/datasets/forecasts/replace/s-1/rows")
    assert staged.body == {"rows": [{"city": "NYC", "high": 51.25}, {"city": "LDN", "high": None}]}
    assert fake.sent("POST", "/datasets/forecasts/replace/s-1/finish")


def test_a_replace_that_fails_drops_what_it_staged(fake: Fake, client: Client) -> None:
    fake.on("POST", "/datasets/forecasts/replace", ok({"stage": "s-1", "staged": 0}, 201))
    fake.on(
        "POST",
        "/datasets/forecasts/replace/s-1/rows",
        refused(400, "invalid_input", "row 0 has no city"),
    )
    fake.on("DELETE", "/datasets/forecasts/replace/s-1", httpx.Response(204))

    with pytest.raises(PolythoughtError, match="has no city"):
        client.datasets.replace("forecasts", [{"high": 1}])

    assert fake.sent("DELETE", "/datasets/forecasts/replace/s-1")
    assert not fake.sent("POST", "/datasets/forecasts/replace/s-1/finish")
