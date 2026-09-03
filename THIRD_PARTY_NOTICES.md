# Third-party notices

The generated SPDX 2.3 SBOM is the machine-readable dependency inventory. This file highlights packages distributed with or linked into preview artifacts; upstream license files remain authoritative.

## Lark/Feishu Go SDK

Source: [larksuite/oapi-sdk-go](https://github.com/larksuite/oapi-sdk-go)

Bundled version: 3.11.0
License: MIT

The Go Feishu Bridge uses the official SDK for the single inbound WebSocket and fixed event dispatch.

## lark-cli

Source: [larksuite/cli](https://github.com/larksuite/cli)

Bundled version: 1.0.92
License: MIT

Platform binaries are downloaded only during packaging from the upstream release and verified against the SHA-256 values in `runtime/lark-cli-runtime.json`.

## Go runtime dependencies

- `golang.org/x/sys` 0.10.0 — BSD-3-Clause
- `github.com/gorilla/websocket` 1.5.0 — BSD-2-Clause
- `github.com/gogo/protobuf` 1.3.2 — BSD-3-Clause

These packages are linked into Go binaries. Their source repositories and license texts are identified by the generated SBOM and Go module metadata.

## Node compatibility runtime

Node.js 24.20.0 is temporarily bundled for the pre-cutover Feishu compatibility implementation. Node.js is distributed under the MIT License with bundled third-party components under their respective licenses. The runtime archive and SHA-256 values are pinned in `runtime/node-runtime.json`.

The compatibility bridge currently includes `@larksuiteoapi/node-sdk` 1.73.0 and `@larksuite/cli` 1.0.92 under their upstream licenses. This runtime is removed only after all Go production-cutover hardware gates pass.

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

CodexAssistant's native Swift iLink adapter is an independent implementation informed by the public protocol and behavior of Tencent's `openclaw-weixin` project. The upstream project is distributed under the MIT License.

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
