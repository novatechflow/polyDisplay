#!/usr/bin/env bash
# Copyright 2026ff novatechflow (Alexander Alten)
# SPDX-License-Identifier: PolyForm-Shield-1.0.0
# Back-compat wrapper; the installer is install.sh (macOS, Linux).
set -euo pipefail
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/install.sh" "$@"
