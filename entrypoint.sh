#!/bin/sh

set -e

# default command, expects 'commit' executable to be available in $PATH
if [ "$1" = 'app' ]; then
  exec commit "${@:2}"
fi

# if arbitrary command was passed, execute it instead of default one
exec "$@"
