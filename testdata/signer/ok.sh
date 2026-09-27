#!/bin/sh
# Test double for a gpg-style signing program: consumes the message on
# standard input and prints a fixed armored block on standard output.
cat >/dev/null
printf '%s\n' '-----BEGIN PGP SIGNATURE-----' '' 'c3R1Yi1zaWduYXR1cmU=' '=AAAA' '-----END PGP SIGNATURE-----'
