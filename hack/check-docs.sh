#!/bin/sh
# Validate without launching Go, converters, models, or database connections.
set -eu
remaining=${DOCS-}
while :; do
    case "$remaining" in
        *';'*) directory=${remaining%%;*}; remaining=${remaining#*;}; more=true ;;
        *) directory=$remaining; more=false ;;
    esac
    directory=$(printf '%s' "$directory" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    if [ -z "$directory" ]; then
        echo 'ERROR: DOCS requires nonempty directories. Usage: make index DOCS="/path/one;/path/two"' >&2
        exit 2
    fi
    if [ ! -d "$directory" ]; then
        printf 'ERROR: DOCS must contain existing directories: %s\n' "$directory" >&2
        exit 2
    fi
    [ "$more" = true ] || break
done
