# Updating dependencies and certificate trust

Update Go modules and tools, both npm manifests and lockfiles, Docker bases,
service images, generated-code tools and CI versions together. Use stable releases
that satisfy upstream compatibility requirements. Run Go tests with race detection,
integration tests, frontend lint/type checks and production builds before deploying.
Compare coverage and CRAP reports on matching source scopes.

## Certificate trust

Updating an SDK does not necessarily update its certificate verifier. Go services
use the Go verifier and either the system trust store or an explicitly configured
private CA. Dashboard Node processes use OpenSSL's system store through
`NODE_OPTIONS=--use-openssl-ca`; their Docker base refreshes the CA packages before
installing dependencies or running the application. Keep this setting when adding
Node options. `--use-system-ca` adds system roots to bundled roots and is not an
equivalent replacement when the bundled snapshot still trusts compromised issuers.

The Dockerfiles require a patched Alpine CA package. Build with refreshed base
images and uncached package installation, then redeploy/restart every affected
process. An already running image does not inherit changes to its base tag or the
package repository. Apply the same trust-store policy to Node processes deployed
outside these Dockerfiles, including static-site build environments.

Validate the application's actual verifier against known compromised chains as
well as a valid control. Browser or curl results do not establish Go or Node trust.
A network timeout, proxy error or missing issuer is not proof that a specific
compromised intermediate was blocked. Review custom public CA bundles separately;
private service CAs must retain their explicit identity and mutual-TLS policy.
Never disable certificate or hostname verification to resolve a trust error.

See [Cloudflare's incident guidance](https://blog.cloudflare.com/emudhra-certificate-compromise/)
and the [affected verifier and trust-store tracker](https://emudhra.we-have-a-problem.com/).
