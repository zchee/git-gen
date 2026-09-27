#!/bin/sh
# Test double for a signing program that fails the way gpg does: a status
# line (with a made-up key id) and a human-readable line on standard error,
# then exit status 1.
cat >/dev/null
printf '%s\n' '[GNUPG:] KEY_CONSIDERED 0123456789ABCDEF0123456789ABCDEF01234567 0' 'gpg: signing failed: No secret key' >&2
exit 1
