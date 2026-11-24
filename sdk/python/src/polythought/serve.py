"""Serving a model: a worker that takes its requests from Polythought and answers them."""

from __future__ import annotations

import logging
import os
import socket
import threading
from collections.abc import Callable, Sequence
from typing import Any

import httpx

from ._json import dumps
from ._version import __version__
from .client import base_url, fail
from .errors import PolythoughtError

log = logging.getLogger("polythought")

#: A model: the items of a request, a list of JSON objects, to one answer each, in order.
Predict = Callable[[list[dict[str, Any]]], Sequence[Any]]

_REASON = 500


def serve(
    predict: Predict | Callable[[Any], Any],
    *,
    key: str | None = None,
    url: str | None = None,
    worker: str | None = None,
    version: str = "",
    frames: bool = False,
    stop: threading.Event | None = None,
    retry: float = 5.0,
    transport: httpx.BaseTransport | None = None,
) -> None:
    """Serves the model a worker key belongs to, until stop is set or the key is refused.

    It calls Polythought for the model's next request, waits for one, and calls predict with
    its items, a list of objects: predict gives one answer each, any JSON, in order. With
    frames, predict gets the items as a pandas DataFrame and gives a DataFrame, a Series or a
    list. An exception predict raises fails the request, saying why, and the worker carries
    on. Polythought unreachable, it tries again after retry seconds.

    The key and the URL come from POLYTHOUGHT_WORKER_KEY and POLYTHOUGHT_URL unless given;
    worker names this worker among the model's, its host and process by default; version is
    what each answer says it came from::

        def predict(items):
            return [{"high": model.predict(i)} for i in items]

        serve(predict, version="1.2.0")
    """
    key = key or os.environ.get("POLYTHOUGHT_WORKER_KEY", "")
    if not key:
        raise ValueError("no worker key: pass key=, or set POLYTHOUGHT_WORKER_KEY")

    stop = stop or threading.Event()
    me = {"worker": worker or f"{socket.gethostname()}-{os.getpid()}", "version": version}

    http = httpx.Client(
        base_url=base_url(url),
        headers={
            "Authorization": f"Bearer {key}",
            "User-Agent": f"polythought-python/{__version__}",
            "Content-Type": "application/json",
        },
        # A call for a request is held open for the instance's poll, 50 s unless an admin
        # says otherwise: read long enough for it.
        timeout=httpx.Timeout(30.0, read=11 * 60.0),
        transport=transport,
    )

    with http:
        log.info("Serving as %s", me["worker"])

        while not stop.is_set():
            try:
                resp = http.post("/worker/next", content=dumps(me))
            except httpx.TransportError as e:
                log.warning("Polythought unreachable, trying again in %ss: %s", retry, e)
                stop.wait(retry)
                continue

            if resp.status_code == 204:  # none came: call again
                continue

            if resp.status_code in (401, 403):
                raise fail(resp)  # the key is wrong or revoked, or its owner kept from models

            if resp.is_error:
                log.warning(
                    "Polythought refused the call, trying again in %ss: %s", retry, fail(resp)
                )
                stop.wait(retry)
                continue

            _answer(http, me, resp.json()["request"], predict, frames)


def _answer(
    http: httpx.Client, me: dict[str, str], request: dict[str, Any], predict: Any, frames: bool
) -> None:
    """Answers one request with what predict gives, or fails it with why it could not."""
    rid, items = request["id"], request["items"]

    try:
        answers = _predict(predict, items, frames)
        if len(answers) != len(items):
            raise ValueError(
                f"{len(answers)} answers for {len(items)} items: one an item, in their order"
            )

        resp = http.post(
            f"/worker/requests/{rid}/answer", content=dumps({**me, "answers": answers})
        )
    except Exception as e:
        reason = (str(e) or type(e).__name__)[:_REASON]
        log.warning("Request %s failed: %s", rid, reason)

        try:
            resp = http.post(
                f"/worker/requests/{rid}/fail", content=dumps({**me, "reason": reason})
            )
        except httpx.TransportError as te:
            log.warning("The failure of %s was not sent: %s", rid, te)
            return

    if resp.status_code == 404:
        log.info("Request %s was answered elsewhere, or its asker stopped waiting", rid)
    elif resp.is_error:
        log.warning("Request %s: %s", rid, fail(resp))


def _predict(predict: Any, items: list[dict[str, Any]], frames: bool) -> list[Any]:
    if not frames:
        return list(predict(items))

    from .frames import frame, pandas, records

    got = predict(frame(items))
    pd_ = pandas()

    if isinstance(got, pd_.DataFrame):
        return records(got)

    if isinstance(got, pd_.Series):
        return list(got.tolist())

    return list(got)


__all__ = ["PolythoughtError", "Predict", "serve"]
