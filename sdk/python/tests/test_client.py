from __future__ import annotations

import pytest

from polythought import Client, PolythoughtError

from .conftest import Fake, ok, refused


def test_needs_a_url_and_a_token(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("POLYTHOUGHT_URL", raising=False)
    monkeypatch.delenv("POLYTHOUGHT_TOKEN", raising=False)

    with pytest.raises(ValueError, match="no API token"):
        Client("https://pt.example")

    with pytest.raises(ValueError, match="no Polythought URL"):
        Client(token="pt_secret")

    monkeypatch.setenv("POLYTHOUGHT_URL", "https://pt.example/")
    monkeypatch.setenv("POLYTHOUGHT_TOKEN", "pt_env")
    Client().close()


def test_sends_its_token_and_says_why_it_is_refused(fake: Fake, client: Client) -> None:
    fake.on(
        "GET", "/datasets", refused(403, "data_off", "datasets and models are not open to you here")
    )

    with pytest.raises(PolythoughtError) as e:
        client.datasets.list()

    assert (e.value.status, e.value.code) == (403, "data_off")
    assert "not open to you" in e.value.message
    assert fake.calls[0].headers["Authorization"] == "Bearer pt_secret"
    assert fake.calls[0].headers["User-Agent"].startswith("polythought-python/")


def test_pages_through_workflows_up_to_the_limit(fake: Fake, client: Client) -> None:
    fake.on(
        "GET",
        "/workflows",
        ok({"workflows": [{"id": "a"}, {"id": "b"}], "nextCursor": "c-1"}),
        ok({"workflows": [{"id": "c"}]}),
    )

    got = client.workflows(active=True)

    assert [w["id"] for w in got] == ["a", "b", "c"]
    first, second = fake.sent("GET", "/workflows")
    assert first.params == {"active": "true", "limit": "100"}
    assert second.params["cursor"] == "c-1"

    fake.on("GET", "/workflows", ok({"workflows": [{"id": "a"}, {"id": "b"}], "nextCursor": "c-1"}))
    assert len(client.workflows(limit=1)) == 1


def test_orders_as_a_table(fake: Fake, client: Client) -> None:
    fake.on(
        "GET",
        "/orders",
        ok(
            {
                "orders": [
                    {"id": "o-1", "price": "0.53", "status": "filled"},
                    {"id": "o-2", "price": "0.4", "status": "filled"},
                ]
            }
        ),
    )

    table = client.orders_frame(workflow="wf-1", status="filled")

    assert list(table["id"]) == ["o-1", "o-2"]
    assert fake.calls[0].params["workflow"] == "wf-1"
