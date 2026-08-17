# Security policy

Please do not report security vulnerabilities in public issues. Send a
private report to the repository maintainer with a description, reproduction
steps, affected version, and any safe mitigation.

Rewind can read operational data and credentials for observability systems.
Treat exported `.rewind` files as sensitive: they may contain service names,
URLs, alert details, log excerpts, and trace references. Review and protect
bundles before attaching them to tickets or sharing them publicly.

The CLI does not send bundle data during offline replay. Live collection uses
the configured source clients and should be run with least-privilege tokens.
