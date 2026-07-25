#!/bin/env bash
# Sipeed LicheeRV Nano (SG2002, linux/riscv64)

dir="$(cd "$(dirname "$0")" && pwd)"
exec "$dir/build-deb.sh" riscv64
