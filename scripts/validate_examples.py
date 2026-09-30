#!/usr/bin/env python3
"""Validate every example configuration against the provider built from this checkout."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


ROOT = Path(__file__).resolve().parents[1]
EXAMPLES = ROOT / "examples"
# Documentation snippets that refer to resources declared in other examples; they are checked by
# terraform fmt only. Keep this list short: a new example should validate on its own.
SNIPPETS = {
    "examples/resources/vew_project_assignment",
    "examples/resources/vew_project_client_assignment",
    "examples/resources/vew_project_group_assignment",
    "examples/resources/vew_recipe_version",
}
REQUIRED_PROVIDERS = (
    'terraform {\n  required_providers {\n    vew = {\n'
    '      source = "elva-labs/vew"\n    }\n  }\n}\n'
)


def example_directories():
    directories = {path.parent for path in EXAMPLES.rglob("*.tf")}
    return sorted(d for d in directories if str(d.relative_to(ROOT)) not in SNIPPETS)


def main():
    subprocess.run(["terraform", "fmt", "-check", "-recursive", str(EXAMPLES)], check=True)
    failures = []
    with tempfile.TemporaryDirectory(prefix=".examples-", dir=ROOT) as temporary:
        work = Path(temporary)
        binary_dir = work / "bin"
        binary_dir.mkdir()
        subprocess.run(
            ["go", "build", "-o", str(binary_dir / "terraform-provider-vew"), "./cmd/terraform-provider-vew"],
            cwd=ROOT,
            check=True,
        )
        config = work / "terraform.rc"
        config.write_text(
            f'provider_installation {{\n  dev_overrides {{\n'
            f'    "elva-labs/vew" = {json.dumps(str(binary_dir))}\n'
            f'  }}\n  direct {{}}\n}}\n'
        )
        env = os.environ.copy()
        env["TF_CLI_CONFIG_FILE"] = str(config)
        for directory in example_directories():
            relative = directory.relative_to(ROOT)
            target = work / "examples" / relative
            shutil.copytree(directory, target)
            # Documentation snippets may omit the provider source; validation needs it.
            if "required_providers" not in "".join(p.read_text() for p in target.glob("*.tf")):
                (target / "zz_required_providers.tf").write_text(REQUIRED_PROVIDERS)
            result = subprocess.run(
                ["terraform", "validate", "-no-color"],
                cwd=target,
                env=env,
                capture_output=True,
                text=True,
            )
            if result.returncode != 0:
                failures.append(f"{relative}\n{result.stdout}{result.stderr}")
    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    print(f"Validated {len(example_directories())} example configurations.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
