"""Values as JSON takes them: numpy's and pandas' too, NaN as null."""

from __future__ import annotations

import datetime as dt
import json
import math
from collections.abc import Mapping
from decimal import Decimal
from typing import Any


def plain(value: Any) -> Any:
    """value as plain JSON values: a NaN or an infinity is null, a numpy scalar its Python
    value, an array or a Series a list, a time its ISO 8601 text."""
    if value is None or isinstance(value, (str, bool, int)):
        return value
    if isinstance(value, float):
        return None if math.isnan(value) or math.isinf(value) else value
    if isinstance(value, Decimal):
        return plain(float(value))
    if isinstance(value, (dt.datetime, dt.date)):
        return value.isoformat()
    if isinstance(value, Mapping):
        return {str(k): plain(v) for k, v in value.items()}
    if isinstance(value, (list, tuple, set, frozenset)):
        return [plain(v) for v in value]
    if hasattr(value, "tolist"):  # a numpy array or a pandas Series
        return plain(value.tolist())
    if hasattr(value, "item"):  # a numpy scalar
        return plain(value.item())
    if hasattr(value, "isoformat"):  # a pandas Timestamp
        return value.isoformat()
    return value


def dumps(value: Any) -> str:
    """value as compact JSON, after plain."""
    return json.dumps(plain(value), separators=(",", ":"), ensure_ascii=False, allow_nan=False)
