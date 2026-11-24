"""Tables as pandas DataFrames, with the pandas extra: pip install "polythought[pandas]"."""

from __future__ import annotations

from collections.abc import Iterable, Mapping
from typing import TYPE_CHECKING, Any, cast

if TYPE_CHECKING:
    import pandas as pd


def pandas() -> Any:
    """The pandas module, or why there is none."""
    try:
        import pandas as pd
    except ImportError as e:
        raise ImportError('tables need pandas: pip install "polythought[pandas]"') from e

    return pd


def frame(records: Iterable[Mapping[str, Any]]) -> pd.DataFrame:
    """records as a table: a row each, a column a field, nested objects kept as they are."""
    return cast("pd.DataFrame", pandas().DataFrame.from_records(list(records)))


def records(table: pd.DataFrame) -> list[dict[str, Any]]:
    """A table's rows as objects, a field a column: what datasets and answers take."""
    return [{str(k): v for k, v in row.items()} for row in table.to_dict(orient="records")]
