"""A fake Polythought: each route answers in turn what a test gives it, and every call is
kept."""

from __future__ import annotations

import json
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Any

import httpx
import pytest

from polythought import Client

Answer = httpx.Response | Callable[[httpx.Request], httpx.Response]


@dataclass
class Call:
    method: str
    path: str
    params: dict[str, str]
    body: Any
    headers: httpx.Headers


@dataclass
class Fake:
    routes: dict[tuple[str, str], list[Answer]] = field(default_factory=dict)
    calls: list[Call] = field(default_factory=list)

    def on(self, method: str, path: str, *answers: Answer) -> None:
        self.routes.setdefault((method, "/api/v1" + path), []).extend(answers)

    def handle(self, request: httpx.Request) -> httpx.Response:
        body = json.loads(request.content) if request.content else None
        self.calls.append(
            Call(request.method, request.url.path, dict(request.url.params), body, request.headers)
        )

        answers = self.routes.get((request.method, request.url.path))
        if not answers:
            return httpx.Response(
                404, json={"error": {"code": "not_found", "message": "no such route"}}
            )

        answer = answers.pop(0) if len(answers) > 1 else answers[0]
        return answer(request) if callable(answer) else answer

    def transport(self) -> httpx.MockTransport:
        return httpx.MockTransport(self.handle)

    def sent(self, method: str, path: str) -> list[Call]:
        return [c for c in self.calls if c.method == method and c.path == "/api/v1" + path]


def ok(body: Any, status: int = 200) -> httpx.Response:
    return httpx.Response(status, json=body)


def refused(status: int, code: str, message: str) -> httpx.Response:
    return httpx.Response(status, json={"error": {"code": code, "message": message}})


@pytest.fixture
def fake() -> Fake:
    return Fake()


@pytest.fixture
def client(fake: Fake) -> Client:
    return Client("https://pt.example", token="pt_secret", transport=fake.transport())
