#!/usr/bin/env python3
"""Generate and check Terraform Registry docs against the live provider schema."""

import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


ROOT = Path(__file__).resolve().parents[1]
DOCS = ROOT / "docs"
PROVIDER_ADDRESS = "registry.terraform.io/elva-labs/vew"
DOCS_TOOL = "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@v0.25.0"


def run(command, *, cwd=ROOT, env=None, stdout=None):
    subprocess.run(command, cwd=cwd, env=env, stdout=stdout, check=True)


def files(directory):
    return {path.relative_to(directory): path.read_bytes()
            for path in directory.rglob("*") if path.is_file()}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("generate", "check"))
    args = parser.parse_args()

    with tempfile.TemporaryDirectory(prefix=".registry-docs-", dir=ROOT) as temporary:
        work = Path(temporary)
        binary_dir = work / "bin"
        binary_dir.mkdir()
        run(["go", "build", "-o", str(binary_dir / "terraform-provider-vew"),
             "./cmd/terraform-provider-vew"])

        terraform_dir = work / "terraform"
        terraform_dir.mkdir()
        (terraform_dir / "main.tf").write_text(
            'terraform {\n  required_providers {\n    vew = {\n'
            '      source = "elva-labs/vew"\n    }\n  }\n}\n'
        )
        config = work / "terraform.rc"
        config.write_text(
            f'provider_installation {{\n  dev_overrides {{\n'
            f'    "elva-labs/vew" = {json.dumps(str(binary_dir))}\n'
            f'  }}\n  direct {{}}\n}}\n'
        )
        env = os.environ.copy()
        env["TF_CLI_CONFIG_FILE"] = str(config)
        schema_path = work / "schema.json"
        with schema_path.open("wb") as output:
            run(["terraform", "providers", "schema", "-json"], cwd=terraform_dir,
                env=env, stdout=output)

        schema = json.loads(schema_path.read_text())
        provider = schema["provider_schemas"].pop(PROVIDER_ADDRESS)
        # tfplugindocs looks for the short provider name in a supplied schema.
        schema["provider_schemas"] = {"vew": provider}
        schema_path.write_text(json.dumps(schema))

        docs_tool = os.environ.get("TFPLUGINDOCS")
        tool = [docs_tool] if docs_tool else ["go", "run", DOCS_TOOL]
        rendered = work / "rendered"
        run(tool + ["generate", "--provider-name", "vew", "--providers-schema",
                    str(schema_path), "--rendered-website-dir",
                    os.path.relpath(rendered, ROOT)])

        if args.mode == "generate":
            if DOCS.exists():
                shutil.rmtree(DOCS)
            shutil.copytree(rendered, DOCS)
            print("Generated docs/ from the current provider schema.")
        else:
            actual, expected = files(DOCS), files(rendered)
            changed = sorted(path for path in actual.keys() | expected.keys()
                             if actual.get(path) != expected.get(path))
            if changed:
                print("Registry documentation is out of date:", file=sys.stderr)
                for path in changed:
                    print(f"  docs/{path}", file=sys.stderr)
                print("Run make docs and commit the generated pages.", file=sys.stderr)
                return 1

        run(tool + ["validate", "--provider-name", "vew", "--providers-schema",
                    str(schema_path)])
        print("Registry documentation matches the provider schema.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
