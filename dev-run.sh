#!/bin/sh
set -eu

tailwindcss -i ./web/static/globals.css -o ./web/static/shadcn.css --minify
exec go run .
