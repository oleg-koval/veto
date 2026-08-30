#!/usr/bin/env python3
"""Small repository-local guard for the required GitHub issue-form contract."""

from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]
FORM_DIR = ROOT / ".github" / "ISSUE_TEMPLATE"
FORMS = ("bug_report.yml", "feature_request.yml", "optimization_proposal.yml")
REQUIRED_IDS = {
    "summary",
    "reproduction_context",
    "expected_behavior",
    "actual_behavior",
    "scope",
    "acceptance_criteria",
}


def validate_form(path: Path) -> list[str]:
    errors: list[str] = []
    text = path.read_text(encoding="utf-8")
    if not re.search(r"^name:\s*\S+", text, re.MULTILINE):
        errors.append("missing name")
    if not re.search(r"^description:\s*\S+", text, re.MULTILINE):
        errors.append("missing description")
    ids = re.findall(r"^\s+id:\s*([A-Za-z0-9_-]+)\s*$", text, re.MULTILINE)
    duplicates = sorted({item for item in ids if ids.count(item) > 1})
    if duplicates:
        errors.append(f"duplicate ids: {', '.join(duplicates)}")
    missing = sorted(REQUIRED_IDS - set(ids))
    if missing:
        errors.append(f"missing ids: {', '.join(missing)}")
    for field in REQUIRED_IDS:
        block = re.search(
            rf"^\s+id:\s*{re.escape(field)}\s*$.*?(?=^\s+- type:|\Z)",
            text,
            re.MULTILINE | re.DOTALL,
        )
        if not block or not re.search(r"required:\s*true", block.group(0)):
            errors.append(f"{field} is not required")
    return errors


def main() -> int:
    errors: list[str] = []
    for name in FORMS:
        path = FORM_DIR / name
        if not path.is_file():
            errors.append(f"missing form: {path}")
            continue
        errors.extend(f"{name}: {error}" for error in validate_form(path))
    if errors:
        print("GitHub issue-form validation failed:", file=sys.stderr)
        print("\n".join(f"- {error}" for error in errors), file=sys.stderr)
        return 1
    print(f"validated {len(FORMS)} GitHub issue forms")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
