"""Your models: served by your workers, or called at a URL."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from typing import TYPE_CHECKING, Any, cast
from urllib.parse import quote

from .types import MLModel, MLModelList, ModelAnswers

if TYPE_CHECKING:
    from .client import Client


class Models:
    """Your models: read with any token, asked with one that may write datasets and models."""

    def __init__(self, client: Client) -> None:
        self._client = client

    def list(self) -> list[MLModel]:
        """Yours, by name, each with the workers connected now."""
        return cast(MLModelList, self._client.get("/models"))["models"]

    def get(self, name: str) -> MLModel:
        return cast(MLModel, self._client.get(f"/models/{quote(name, safe='')}"))

    def ask(
        self,
        name: str,
        items: Sequence[Mapping[str, Any]],
        *,
        schema: Mapping[str, Any] | None = None,
        wait: int = 30,
    ) -> ModelAnswers:
        """Asks a model about items, as a workflow does, and waits up to wait seconds for its
        answers, one an item. Those that do not keep to schema come back under refused."""
        body: dict[str, Any] = {"items": list(items), "waitSeconds": wait}
        if schema is not None:
            body["schema"] = schema

        resp = self._client.request(
            "POST", f"/models/{quote(name, safe='')}/ask", body=body, timeout=wait + 30
        )
        return cast(ModelAnswers, resp.json())
