#!/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

personal_user="la""wis"
absolute_user_prefix="/""Users/"
personal_bundle="com.""lawis"
personal_certificate="Codex Usage Bar Local ""Signing"

for pattern in "$personal_user" "$absolute_user_prefix" "$personal_bundle" "$personal_certificate"; do
    if rg -n --glob '!scripts/check-release-hygiene.sh' --fixed-strings "$pattern" Sources Resources scripts Package.swift README.md PRODUCT.md DESIGN.md AGENTS.md; then
        echo "Release hygiene failed: found forbidden machine-specific value '$pattern'." >&2
        exit 1
    fi
done

if rg -n -g '!Tests/**' -g '!dist/**' -g '!.build/**' \
    'BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|AKIA[0-9A-Z]{16}|glpat-[A-Za-z0-9_-]+' .; then
    echo "Release hygiene failed: found a credential-like value." >&2
    exit 1
fi

if git ls-files | rg '(^|/)(dist|\.build)/|token-history-v[0-9]+\.json$|wechat-state-v[0-9]+\.enc$|feishu-bridge/client\.json$'; then
    echo "Release hygiene failed: runtime or build data is tracked by Git." >&2
    exit 1
fi

echo "PASS release hygiene"
