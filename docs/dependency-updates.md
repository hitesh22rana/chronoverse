# Updating dependencies and certificate trust

Update Go modules and tools, npm manifests and lockfiles, Docker bases, service
images, generated-code tools and CI versions together. Use stable releases that
satisfy upstream compatibility requirements. Check release notes and regenerate
code and lockfiles with the selected tools.

Run Go tests with race detection, integration tests, frontend lint/type checks and
production builds. Compare coverage and CRAP reports on matching source scopes.
Record selected versions, compatibility constraints and validation results in the
pull request rather than maintaining release snapshots in this guide.

## Certificate trust

An SDK may delegate certificate verification to a language runtime, the operating
system or a custom trust store. Review each layer when updating dependencies.
Updating an SDK alone does not necessarily update its certificate verifier.

Go services use the Go verifier with system roots or an explicitly configured
private CA. Dashboard Node processes select OpenSSL's system store through
`NODE_OPTIONS=--use-openssl-ca`. Keep this setting when adding Node options.
`--use-system-ca` combines system and bundled roots; it selects a different trust
policy and is not an equivalent replacement.

Refresh base images and CA packages when rebuilding containers. Redeploy or restart
processes to activate runtime and trust-store updates. Running images do not inherit
changes to their base tags or package repositories. Apply the same trust policy to
build environments and processes running outside the project's Dockerfiles.

Validate the application's actual verifier using a trusted certificate chain and
chains it must reject. Browser or curl results do not establish another runtime's
trust behavior. Distinguish certificate rejection from DNS failures, proxy errors,
timeouts and incomplete test chains.

Review custom CA bundles separately. Preserve private service identities and
mutual-TLS requirements. Never disable certificate or hostname verification to
resolve a trust error.
