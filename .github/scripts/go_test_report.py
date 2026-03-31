import argparse
import html
import json
import re
from collections import defaultdict
from dataclasses import dataclass
from pathlib import Path, PurePosixPath


@dataclass
class FileCoverage:
    total_stmts: int = 0
    covered_stmts: int = 0

    @property
    def missed_stmts(self) -> int:
        return self.total_stmts - self.covered_stmts

    @property
    def percent(self) -> int:
        if self.total_stmts <= 0:
            return 0
        return int(round((self.covered_stmts / self.total_stmts) * 100))


COVERAGE_RE = re.compile(
    r"^(?P<path>.+?):(?P<start_line>\d+)\.(?P<start_col>\d+),"
    r"(?P<end_line>\d+)\.(?P<end_col>\d+)\s+"
    r"(?P<num_stmts>\d+)\s+(?P<count>\d+)$"
)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Generate a markdown test report for GitHub comments.")
    parser.add_argument("--test-json", required=True, type=Path)
    parser.add_argument("--coverprofile", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--run-url", default="")
    return parser.parse_args()


def load_test_counts(path: Path) -> tuple[int, int, int, int]:
    passed = failures = errors = skipped = 0
    package_has_failed_test: dict[str, bool] = defaultdict(bool)
    package_failed: dict[str, bool] = defaultdict(bool)

    if not path.exists():
        return passed, failures, errors, skipped

    with path.open(encoding="utf-8") as handle:
        for raw_line in handle:
            line = raw_line.strip()
            if not line:
                continue
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                continue

            action = event.get("Action")
            package = event.get("Package") or ""
            test_name = event.get("Test")

            if test_name:
                if action == "pass":
                    passed += 1
                elif action == "fail":
                    failures += 1
                    package_has_failed_test[package] = True
                elif action == "skip":
                    skipped += 1
                continue

            if action == "fail" and package:
                package_failed[package] = True

    errors = sum(1 for package, failed in package_failed.items() if failed and not package_has_failed_test[package])
    return passed, failures, errors, skipped


def load_coverage(path: Path) -> dict[str, FileCoverage]:
    files: dict[str, FileCoverage] = {}

    if not path.exists():
        return files

    with path.open(encoding="utf-8") as handle:
        for raw_line in handle:
            line = raw_line.strip()
            if not line or line.startswith("mode:"):
                continue
            match = COVERAGE_RE.match(line)
            if not match:
                continue
            file_path = match.group("path")
            num_stmts = int(match.group("num_stmts"))
            count = int(match.group("count"))
            stats = files.setdefault(file_path, FileCoverage())
            stats.total_stmts += num_stmts
            if count > 0:
                stats.covered_stmts += num_stmts

    return files


def emoji_for_percent(percent: int) -> str:
    if percent < 60:
        return "🔴"
    if percent < 70:
        return "🟡"
    return "🟢"


def group_coverage_files(files: dict[str, FileCoverage]) -> list[tuple[str, list[tuple[str, FileCoverage]]]]:
    groups: dict[str, list[tuple[str, FileCoverage]]] = defaultdict(list)
    for file_path, coverage in files.items():
        if coverage.total_stmts <= 0 or coverage.percent >= 100:
            continue
        parent = str(PurePosixPath(file_path).parent)
        group = "(root)" if parent in {"", "."} else parent
        groups[group].append((file_path, coverage))

    def group_sort_key(name: str) -> tuple[int, str]:
        return (0 if name == "(root)" else 1, name)

    grouped: list[tuple[str, list[tuple[str, FileCoverage]]]] = []
    for group_name in sorted(groups, key=group_sort_key):
        entries = sorted(
            groups[group_name],
            key=lambda item: (item[1].percent, item[0]),
        )
        grouped.append((group_name, entries))
    return grouped


def build_report(
    passed: int,
    failures: int,
    errors: int,
    skipped: int,
    files: dict[str, FileCoverage],
    run_url: str,
) -> str:
    total = passed + failures + errors + skipped
    covered_stmts = sum(item.covered_stmts for item in files.values())
    total_stmts = sum(item.total_stmts for item in files.values())
    coverage_percent = int(round((covered_stmts / total_stmts) * 100)) if total_stmts else 0
    files_missing = [item for item in files.values() if item.total_stmts > 0 and item.percent < 100]

    lines: list[str] = []
    lines.append("### ✅ Test Report")
    lines.append("")
    lines.append("| Passed | Failures | Errors | Skipped | Total | Coverage | Files < 100% |")
    lines.append("| ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
    lines.append(
        f"| {passed} | {failures} | {errors} | {skipped} | {total} | **{coverage_percent}%** | {len(files_missing)} |"
    )
    lines.append("")
    lines.append("### 📁 Files missing full coverage (grouped)")
    lines.append("")

    grouped = group_coverage_files(files)
    if not grouped:
        lines.append("No files below 100% coverage.")
    else:
        for group_name, entries in grouped:
            lines.append("<details>")
            lines.append(
                f"<summary>📂 <code>{html.escape(group_name)}</code> ({len(entries)} files)</summary>"
            )
            lines.append("")
            for file_path, coverage in entries:
                file_name = html.escape(PurePosixPath(file_path).name)
                lines.append(
                    f"- {emoji_for_percent(coverage.percent)} `{file_name}` — {coverage.percent}% "
                    f"(missed: {coverage.missed_stmts})"
                )
            lines.append("")
            lines.append("</details>")
            lines.append("")

    if run_url:
        lines.append(f"🔗 [View run logs]({run_url})")

    lines.append("")
    lines.append("<!-- ci-report:go -->")
    return "\n".join(lines).rstrip() + "\n"


def main() -> None:
    args = parse_args()
    passed, failures, errors, skipped = load_test_counts(args.test_json)
    files = load_coverage(args.coverprofile)
    report = build_report(passed, failures, errors, skipped, files, args.run_url)
    args.output.write_text(report, encoding="utf-8")


if __name__ == "__main__":
    main()
