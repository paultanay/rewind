# Security policy

Rewind is an early open-source project. Security reports are welcome, and
responsible disclosure helps protect people who use Rewind with operational
data.

## Where to report

Please do not disclose a security vulnerability in a public issue, discussion,
pull request, or social-media post. Public disclosure can expose users before
a fix is available.

Use GitHub's private vulnerability reporting form:

<https://github.com/paultanay/rewind/security/advisories/new>

If the form is unavailable, contact the maintainer privately through GitHub
without including sensitive details in a public comment. Public issues and
discussions are appropriate for ordinary bugs, feature requests, questions,
and design proposals that do not contain confidential information.

## What to include

Please provide:

- a concise description of the issue and its security impact;
- the affected version, commit, or configuration;
- reproducible steps or a minimal proof of concept;
- the source, operating system, and deployment context involved; and
- any known workaround or mitigation.

Please avoid attaching real incident bundles, credentials, tokens, private
URLs, customer data, or unredacted logs. Use synthetic fixtures or carefully
redacted examples instead.

We will acknowledge reports when they are received, investigate them in
private, and coordinate disclosure of the fix with the reporter. Response
times may vary because Rewind is currently maintained by a small team.

## Security boundaries

Rewind can read operational data and credentials for configured observability
systems. Use read-only credentials with the smallest practical scope.

Exported `.rewind` bundles can contain service names, URLs, alert details, log
excerpts, trace references, and other sensitive incident evidence. Protect
bundles as confidential data and review them before sharing.

The local UI binds to loopback and has no authentication layer. Do not expose
it on a shared interface or reverse proxy without adding access control.

Offline replay does not contact live observability systems. Live collection
uses the configured source clients and should be run only in an environment
where those source credentials and results are appropriately protected.

## Safe testing

Only test systems and data you own or are explicitly authorized to assess. Do
not access another user's data, exfiltrate credentials, destroy evidence, or
degrade a production service while validating a report.
