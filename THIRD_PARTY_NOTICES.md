# Third-party notices

The generated SPDX 2.3 SBOM is the machine-readable dependency inventory. This file highlights packages distributed with or linked into preview artifacts; upstream license files remain authoritative.

## lark-cli

Source: [larksuite/cli](https://github.com/larksuite/cli)

Bundled version: 1.0.93
License: MIT

Platform binaries are downloaded only during packaging from the upstream release and verified against the SHA-256 values in `runtime/lark-cli-runtime.json`. The 28 upstream Skills and their MIT license come from the same v1.0.93 source tag, with original per-file provenance in `runtime/lark-skills.json`. KSFAssistant distributes entry-adapted versions, preserving the upstream license and domain guidance; the packaged adaptation report records original/final hashes and adapter sources. The app does not independently update these components.

Core no longer directly links the Lark Go SDK. The official CLI embeds its own Go dependencies (including its SDK); the generated SBOM records those as CLI supply-chain contents, not Core dependencies. Post-signing binary hashes are recorded separately from upstream download hashes.

## Go runtime dependencies

- `golang.org/x/sys` 0.10.0 — BSD-3-Clause

These packages are linked into Go binaries. Their source repositories and license texts are identified by the generated SBOM and Go module metadata.

## Frozen Node compatibility tests

`Services/FeishuBridge` retains `@larksuiteoapi/node-sdk` 1.73.0 and `@larksuite/cli` 1.0.92 solely as a frozen offline regression baseline under their upstream licenses. They are not installed as a production service or bundled with the macOS application. Node.js is a build/test dependency; Windows Electron supplies its own host runtime.

## go-winio

Source: [Microsoft/go-winio](https://github.com/microsoft/go-winio)
Bundled version: 0.6.2

The Windows shared core uses `go-winio` for the optional current-user Codex Desktop named-pipe transport.

```text
The MIT License (MIT)

Copyright (c) 2015 Microsoft

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

The Windows package also contains Electron's own license and Chromium third-party notices as `LICENSE.electron.txt` and `LICENSES.chromium.html`. Electron Builder is used only during packaging and is not shipped as application code.

## Codex (OpenAI) status icon

Source: [theSVG — Codex (OpenAI)](https://thesvg.org/icon/codex-openai)  
Source file: `public/icons/codex-openai/default.svg`  
Retrieved: 2026-08-30  
Upstream SHA-256: `5f424b10216e17cd79c5f852138969453e031066e68a8d9c661e74534276ed9c`  
Bundled SHA-256: `eebed8c9c7acb866747ee0ff06f1005e7ffa4388d704a56f1921f9067d6c6fef` (path separators normalized for macOS CoreSVG; geometry unchanged)

The icon is distributed by theSVG under the MIT License. “Codex”, “OpenAI”, and their marks belong to their respective owner. Inclusion identifies compatibility only and does not imply endorsement.

```text
MIT License

Copyright (c) 2025 thesvg.org

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Tencent openclaw-weixin protocol reference

Source: [Tencent/openclaw-weixin](https://github.com/Tencent/openclaw-weixin)  
Referenced version: 2.4.6  
Retrieved: 2026-08-30

KSFAssistant's native Swift iLink adapter is an independent implementation informed by the public protocol and behavior of Tencent's `openclaw-weixin` project. The upstream project is distributed under the MIT License.

```text
Copyright (C) 2026 Tencent. All rights reserved.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
