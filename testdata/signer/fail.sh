#!/bin/sh
# Test double for a signing program that fails the way gpg does: a status
# line (with a made-up key id) and human-readable lines on standard error,
# one of which quotes the key it was given as its last argument, then exit
# status 1.
for key do :; done
cat >/dev/null
printf '%s\n' '[GNUPG:] KEY_CONSIDERED 0123456789ABCDEF0123456789ABCDEF01234567 0' >&2
printf 'gpg: skipped "%s": No secret key\n' "$key" >&2
printf '%s\n' 'gpg: signing failed: No secret key' >&2
exit 1
