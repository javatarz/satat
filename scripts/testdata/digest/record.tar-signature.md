# Call on 2026-10-28

Planted record: a 19-byte header line puts the tar magic below at byte 257,
where gitleaks sniffs for a tar file. The last line stands for a token.

Signature line: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxustar xxxxxxxxxxxxxxxxxxxx

Regression fixture for the old 19-byte scan header; today's header outgrows gitleaks's first chunk.

- token: @TOKEN@
