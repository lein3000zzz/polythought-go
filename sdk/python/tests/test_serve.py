from __future__ import annotations

import threading
from typing import Any

import httpx
import pandas as pd
import pytest

from polythought import PolythoughtError, serve

from .conftest import Fake, ok, refused


def request(rid: str, *items: dict[str, Any]) -> httpx.Response:
    return ok(
        {
            "model": "temps",
            "request": {"id": rid, "items": list(items), "deadline": "2026-11-24T12:00:30Z"},
        }
    )


def run(fake: Fake, predict: Any, **kwargs: Any) -> threading.Event:
    """Serves until the fake has answered every next it was given, then stops."""
    stop = threading.Event()
    nexts = fake.routes[("POST", "/api/v1/worker/next")]

    def stopping(req: httpx.Request) -> httpx.Response:
        stop.set()
        return httpx.Response(204)

    nexts.append(stopping)
    serve(
        predict,
        key="pw_key",
        url="https://pt.example",
        worker="gpu-1",
        version="1.2.0",
        stop=stop,
        retry=0,
        transport=fake.transport(),
        **kwargs,
    )

    return stop


def test_answers_each_request_with_what_predict_gives(fake: Fake) -> None:
    fake.on(
        "POST",
        "/worker/next",
        request("r-1", {"city": "NYC"}, {"city": "LDN"}),
        httpx.Response(204),
    )
    fake.on("POST", "/worker/requests/r-1/answer", httpx.Response(204))

    run(fake, lambda items: [{"high": 51.25 if i["city"] == "NYC" else 9} for i in items])

    took = fake.sent("POST", "/worker/next")[0]
    assert took.body == {"worker": "gpu-1", "version": "1.2.0"}
    assert took.headers["Authorization"] == "Bearer pw_key"

    (answered,) = fake.sent("POST", "/worker/requests/r-1/answer")
    assert answered.body == {
        "worker": "gpu-1",
        "version": "1.2.0",
        "answers": [{"high": 51.25}, {"high": 9}],
    }


def test_a_predict_that_fails_fails_its_request_and_the_worker_carries_on(fake: Fake) -> None:
    fake.on(
        "POST", "/worker/next", request("r-1", {"city": "NYC"}), request("r-2", {"city": "LDN"})
    )
    fake.on("POST", "/worker/requests/r-1/fail", httpx.Response(204))
    fake.on("POST", "/worker/requests/r-2/fail", httpx.Response(204))

    def predict(items: list[dict[str, Any]]) -> list[Any]:
        if items[0]["city"] == "NYC":
            raise RuntimeError("out of memory")
        return []  # one short

    run(fake, predict)

    assert fake.sent("POST", "/worker/requests/r-1/fail")[0].body["reason"] == "out of memory"
    assert (
        "0 answers for 1 items" in fake.sent("POST", "/worker/requests/r-2/fail")[0].body["reason"]
    )


def test_frames_and_numpy_answers(fake: Fake) -> None:
    fake.on(
        "POST",
        "/worker/next",
        request("r-1", {"city": "NYC", "temp": 50}, {"city": "LDN", "temp": 8}),
    )
    fake.on("POST", "/worker/requests/r-1/answer", httpx.Response(204))

    run(fake, lambda table: table["temp"] + 1.5, frames=True)

    (answered,) = fake.sent("POST", "/worker/requests/r-1/answer")
    assert answered.body["answers"] == [51.5, 9.5]

    fake.calls.clear()
    fake.routes.clear()
    fake.on("POST", "/worker/next", request("r-2", {"city": "NYC"}))
    fake.on("POST", "/worker/requests/r-2/answer", httpx.Response(204))

    run(fake, lambda table: pd.DataFrame({"city": table["city"], "go": [True]}), frames=True)
    assert fake.sent("POST", "/worker/requests/r-2/answer")[0].body["answers"] == [
        {"city": "NYC", "go": True}
    ]


def test_a_late_answer_is_let_go(fake: Fake) -> None:
    fake.on("POST", "/worker/next", request("r-1", {"city": "NYC"}))
    fake.on(
        "POST", "/worker/requests/r-1/answer", refused(404, "not_found", "no such request waits")
    )

    run(fake, lambda items: [1])  # no raise: the worker carries on


def test_unreachable_it_tries_again(fake: Fake) -> None:
    def down(req: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("refused", request=req)

    fake.on("POST", "/worker/next", down, request("r-1", {"city": "NYC"}))
    fake.on("POST", "/worker/requests/r-1/answer", httpx.Response(204))

    run(fake, lambda items: [1])
    assert fake.sent("POST", "/worker/requests/r-1/answer")


def test_a_wrong_or_revoked_key_stops_it(fake: Fake) -> None:
    fake.on("POST", "/worker/next", refused(401, "unauthenticated", "not authenticated"))

    with pytest.raises(PolythoughtError) as e:
        serve(
            lambda items: items,
            key="pw_wrong",
            url="https://pt.example",
            transport=fake.transport(),
        )

    assert e.value.status == 401


def test_needs_a_key(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("POLYTHOUGHT_WORKER_KEY", raising=False)

    with pytest.raises(ValueError, match="no worker key"):
        serve(lambda items: items, url="https://pt.example")
