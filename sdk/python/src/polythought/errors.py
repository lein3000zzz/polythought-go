"""What Polythought answers when it does not do what was asked."""

from __future__ import annotations


class PolythoughtError(Exception):
    """An answer that is no success: its HTTP status, its code, like not_found or data_off,
    and its message."""

    def __init__(self, status: int, code: str, message: str) -> None:
        super().__init__(f"{status} {code}: {message}")
        self.status = status
        self.code = code
        self.message = message
