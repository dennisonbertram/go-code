#!/usr/bin/env bash
set -euo pipefail
echo -e "alice 10\nbob 5\ncarol 20\ndave 15" | awk '{print $2, $1}' | sort -k1 -n | awk '{print $2" -> "$1}' > ranked.txt
cat ranked.txt
