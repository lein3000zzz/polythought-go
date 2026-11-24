"""Your datasets: named tables of JSON rows, which notebooks publish and workflows read, or
workflows record and notebooks read."""

from __future__ import annotations

import builtins
import contextlib
from collections.abc import Iterable, Iterator, Mapping
from typing import TYPE_CHECKING, Any, cast
from urllib.parse import quote

from ._json import dumps
from .errors import PolythoughtError
from .types import Dataset, DatasetList, DatasetRow, DatasetRows, DatasetStage

if TYPE_CHECKING:
    import pandas as pd

    from .client import Client

# A request takes at most 1 MiB: rows go in batches under it.
_BATCH_BYTES = 900_000
_BATCH_ROWS = 1000
_PAGE = 1000


def _path(name: str, *rest: str) -> str:
    return "/".join(["/datasets", quote(name, safe=""), *rest])


def batches(rows: Iterable[Mapping[str, Any]]) -> Iterator[list[Mapping[str, Any]]]:
    """rows in batches each small enough for one request."""
    batch: list[Mapping[str, Any]] = []
    size = 0

    for row in rows:
        n = len(dumps(row).encode()) + 1
        if batch and (size + n > _BATCH_BYTES or len(batch) >= _BATCH_ROWS):
            yield batch
            batch, size = [], 0

        batch.append(row)
        size += n

    if batch:
        yield batch


class Datasets:
    """Your datasets: read with any token, written with one that may write datasets."""

    def __init__(self, client: Client) -> None:
        self._client = client

    def list(self) -> list[Dataset]:
        """Yours, by name."""
        return cast(DatasetList, self._client.get("/datasets"))["datasets"]

    def get(self, name: str) -> Dataset:
        return cast(Dataset, self._client.get(_path(name)))

    def create(
        self, name: str, *, key_field: str | None = None, keep_days: int = 0, keep_rows: int = 0
    ) -> Dataset:
        """Makes one. Its name, fixed, is lower-case letters, digits, - and _. With a key
        field, every row has it, and rows are looked up by it. keep_days and keep_rows drop
        the oldest rows past them; 0 keeps them all."""
        body = {
            "name": name,
            "keyField": key_field or "",
            "keepDays": keep_days,
            "keepRows": keep_rows,
        }
        return cast(Dataset, self._client.request("POST", "/datasets", body=body).json())

    def delete(self, name: str) -> None:
        """Deletes one and its rows: your workflows on naming it are switched off."""
        self._client.request("DELETE", _path(name))

    def rows(self, name: str, *, after: int = 0) -> Iterator[DatasetRow]:
        """Its rows, oldest first, a page at a time."""
        while True:
            page = cast(
                DatasetRows, self._client.get(_path(name, "rows"), after=after, limit=_PAGE)
            )
            yield from page["rows"]

            nxt = page.get("next")
            if nxt is None:
                return

            after = nxt

    def read(self, name: str) -> builtins.list[dict[str, Any]]:
        """Every row, oldest first, each the object it is."""
        return [dict(r["row"]) for r in self.rows(name)]

    def frame(self, name: str) -> pd.DataFrame:
        """Every row as a table: a row each, a column a field."""
        from .frames import frame

        return frame(self.read(name))

    def row(self, name: str, key: str) -> dict[str, Any] | None:
        """The row last added with the key, in a dataset with a key field; None for none."""
        try:
            got = cast(DatasetRow, self._client.get(_path(name, "rows", quote(key, safe=""))))
        except PolythoughtError as e:
            if e.code == "not_found" and "no row" in e.message:
                return None
            raise

        return dict(got["row"])

    def append(self, name: str, rows: Iterable[Mapping[str, Any]]) -> Dataset:
        """Adds rows, in batches: past its Keep rows the oldest go; without it, rows past the
        instance's most are refused."""
        d: Dataset | None = None
        for batch in batches(rows):
            d = cast(
                Dataset,
                self._client.request("POST", _path(name, "rows"), body={"rows": batch}).json(),
            )

        return d if d is not None else self.get(name)

    def replace(self, name: str, rows: Iterable[Mapping[str, Any]]) -> Dataset:
        """Replaces every row at once: the rows are staged in batches, then swapped in
        together, so workflows reading it never see it half-written. A failure drops what
        was staged and leaves the dataset as it was."""
        stage = cast(DatasetStage, self._client.request("POST", _path(name, "replace")).json())[
            "stage"
        ]

        try:
            for batch in batches(rows):
                self._client.request(
                    "POST", _path(name, "replace", stage, "rows"), body={"rows": batch}
                )

            return cast(
                Dataset,
                self._client.request("POST", _path(name, "replace", stage, "finish")).json(),
            )
        except BaseException:
            # Gone already, dropped by itself or never there: nothing to drop.
            with contextlib.suppress(PolythoughtError):
                self._client.request("DELETE", _path(name, "replace", stage))
            raise

    def append_frame(self, name: str, table: pd.DataFrame) -> Dataset:
        """Adds a table's rows: a row each, a field a column."""
        from .frames import records

        return self.append(name, records(table))

    def replace_frame(self, name: str, table: pd.DataFrame) -> Dataset:
        """Replaces every row with a table's, at once."""
        from .frames import records

        return self.replace(name, records(table))
