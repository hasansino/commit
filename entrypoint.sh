#!/bin/sh

set -e

# default command, expects 'commit' executable to be available in $PATH
if [ "$1" = 'commit' ]; then
  shift
  exec commit "$@"
fi

# if arbitrary command was passed, execute it instead of default one
exec "$@"
