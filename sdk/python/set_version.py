"""Sets the SDK's version from Polythought's, as a release tags it, for its wheel.

Polythought's versions are semver, like 0.1.0 or 0.1.0-beta.1; Python's, PEP 440, like
0.1.0b1. A build from the code, dev, is 0.0.0.dev0.

    python set_version.py 0.1.0-beta.1
"""

import re
import sys
from pathlib import Path


def pep440(version: str) -> str:
    version = version.strip().removeprefix("v")
    m = re.fullmatch(r"(\d+\.\d+\.\d+)(?:-(alpha|beta|rc)\.?(\d+))?", version)
    if not m:
        return "0.0.0.dev0"

    release, kind, n = m.groups()
    if not kind:
        return release

    return release + {"alpha": "a", "beta": "b", "rc": "rc"}[kind] + n


if __name__ == "__main__":
    path = Path(__file__).parent / "src" / "polythought" / "_version.py"
    text = path.read_text()
    path.write_text(re.sub(r'__version__ = ".*"', f'__version__ = "{pep440(sys.argv[1])}"', text))
