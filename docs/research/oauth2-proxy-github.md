# Research: oauth2-proxy GitHub auth (T6, issue #25)

Primary sources: the oauth2-proxy docs (7.15.x) and upstream source
(`oauth2-proxy/oauth2-proxy`, default branch `main`, inspected 2026-10-07). Facts
below were read from source, not assumed.

## Image

- `quay.io/oauth2-proxy/oauth2-proxy:v7.15.5` (latest stable; tag verified on Quay).
- Default listen address is `127.0.0.1:4180`, which is **not** reachable from another
  container. Must set `OAUTH2_PROXY_HTTP_ADDRESS=0.0.0.0:4180`.

## Username allowlist (the T6 requirement)

The issue asks for a "comma-separated GitHub usernames" allowlist. The GitHub provider
has `--github-user` (config field `github_users`, type string | list). The docs describe
it as an *additional* allowance ("users are allowed to log in even if they do not belong
to the specified org/team/collaborators"), which reads as non-restrictive. Source says
otherwise.

From `providers/github.go`:

```go
func (p *GitHubProvider) checkUserRestriction(ctx, s) (bool, error) {
    if len(p.Users) == 0 { return false, nil }
    verifiedUser, err := p.hasUser(ctx, s.AccessToken)
    if err != nil { return verifiedUser, err }
    // org and repository options are not configured
    if !verifiedUser && p.Org == "" && p.Repo == "" {
        return false, errors.New("missing github user")
    }
    return verifiedUser, nil
}

func (p *GitHubProvider) checkRestrictions(ctx, s) error {
    if ok, err := p.checkUserRestriction(ctx, s); err != nil || ok { return err }
    // ... org/team/repo checks
}
```

So **with `github_users` set and no org/repo configured, a user not in the list is
rejected** (`missing github user`). That is exactly a pure username allowlist. The
docstring's "additional" wording only applies when org/team/repo *is* also set.

Consequence: if `OAUTH2_ALLOWLIST` is empty, `len(Users) == 0`, the restriction is
skipped and **any** GitHub account is admitted. The deploy must therefore fail fast on an
empty allowlist (it does — `: "${OAUTH2_ALLOWLIST:?}"`).

## Required settings

From `pkg/validation/options.go`, validation requires one of
`email-domain` / `authenticated-emails-file` / `htpasswd-file`:

```
missing setting for email validation: email-domain or authenticated-emails-file required.
      use email-domain=* to authorize all email addresses
```

So `OAUTH2_PROXY_EMAIL_DOMAINS=*` is mandatory with GitHub auth. No upstream is required
(`validateUpstreams` over an empty list yields no errors); `static://202` is set to keep a
defined "authenticated" response for non-auth paths.

## forward_auth (Caddy)

Caddy's native `forward_auth` (2.7+) sends the request to oauth2-proxy's `/oauth2/auth`
and treats any 2xx as authenticated. To surface the identity and to redirect on 401:

```
(oauth2) {
    forward_auth oauth2-proxy:4180 {
        uri /oauth2/auth
        copy_headers X-Auth-Request-User X-Auth-Request-Email
        @unauthorized status 401
        handle_response @unauthorized {
            redir * /oauth2/sign_in?rd={scheme}://{host}{uri}
        }
    }
}
```

- `copy_headers X-Auth-Request-*` requires `--set-xauthrequest` (`OAUTH2_PROXY_SET_XAUTHREQUEST=true`).
- oauth2-proxy must know it is behind TLS: `--reverse-proxy=true` + `--cookie-secure=true`.
- `/oauth2/*` (sign_in, callback, start, auth) must reach oauth2-proxy **without** the
  forward_auth gate, so it gets its own `handle /oauth2/*` route.

## Decision: configure via environment variables, drop the `.tmpl` file

The T6 spec proposed `gateway/oauth2-proxy.yaml.tmpl`. Two problems:

1. oauth2-proxy's `--config` file is the **legacy TOML/cfg** format, not YAML; the YAML
   format is only the experimental `--alpha-config`, which forbids the provider flags we
   need and is explicitly unstable.
2. Every setting has an `OAUTH2_PROXY_*` environment-variable form, and list values
   (like `github_users`) pass cleanly as a single comma-separated env var — no
   templating/quoting of a list.

So, like T5 dropping `canvas-config.yaml.tmpl`, T6 configures oauth2-proxy entirely
through `OAUTH2_PROXY_*` env vars set in compose and sourced from `/opt/satat/.env`
(mode 0600). The `gateway/oauth2-proxy.yaml.tmpl` file is dropped. Non-secret settings
(`provider`, `email_domains`, `http_address`, `reverse_proxy`, cookie/header flags,
`static://202`, plus the domain-derived `redirect_url`/`whitelist_domains`) are literals
in `deploy/docker-compose.yml`; the allowlist, client id, and the two secrets come from
`.env`.
