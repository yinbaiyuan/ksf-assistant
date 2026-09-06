#!/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_root"

personal_user="la""wis"
personal_bundle="com.""lawis"
personal_certificate="KSFAssistant Local ""Signing"
legacy_certificate="CodexAssistant Local ""Signing"
private_host="gitlab.""houzzkit.com"
personal_name="尹""超"

for pattern in "$personal_user" "$personal_bundle" "$personal_certificate" "$legacy_certificate" "$private_host" "$personal_name"; do
    if rg -n \
        --glob '!scripts/check-release-hygiene.sh' \
        --glob '!Windows/node_modules/**' \
        --glob '!Windows/dist/**' \
        --fixed-strings "$pattern" \
        Sources Resources Core Windows docs scripts Services runtime Package.swift README.md PRODUCT.md DESIGN.md AGENTS.md \
        CONTRIBUTING.md SECURITY.md PRIVACY.md SUPPORT.md CODE_OF_CONDUCT.md THIRD_PARTY_NOTICES.md; then
        echo "Release hygiene failed: found forbidden machine-specific value '$pattern'." >&2
        exit 1
    fi
done

if rg -n -g '!Tests/**' -g '!dist/**' -g '!.build/**' \
    'BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|AKIA[0-9A-Z]{16}|glpat-[A-Za-z0-9_-]+' .; then
    echo "Release hygiene failed: found a credential-like value." >&2
    exit 1
fi

if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    inventory="$(git ls-files)"
else
    inventory="$(find . -type f -print | sed 's#^\./##')"
fi
if printf '%s\n' "$inventory" | rg '(^|/)(dist|\.build)/|token-history-v[0-9]+\.json$|wechat-state-v[0-9]+\.enc$|feishu-bridge/client\.json$'; then
    echo "Release hygiene failed: runtime or build data is tracked by Git." >&2
    exit 1
fi

echo "PASS release hygiene"
