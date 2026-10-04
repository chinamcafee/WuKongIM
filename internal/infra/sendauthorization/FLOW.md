# Personal Send Account Admission

The adapter implements `message.PersonalSendAuthorizer` using one private signed
HTTP callback per recipient. It reads no cluster or account database itself.
The app composition root configures the fixed URL, HMAC key, timeout and bounded
concurrency. Metadata friendship ACLs still run normally. A decision is checked
after plugin mutation and before the unchanged cluster append router.

Successful decisions are never cached. HMAC binds POST, exact endpoint path,
timestamp, UUID nonce and SHA-256 of the exact JSON body. HTTP failures, malformed
responses, missing booleans, cancelled requests and concurrency pressure reject
admission. Redirects are disabled. Requests carry only sender/recipient UIDs and
a trusted system-recipient flag, never message content, tokens or user identity.

The callback is optional in general WuKongIM deployments; the Link-U deployment
must enable it. Trusted system senders retain security/rights notification paths.
Already admitted messages and historical message sync are not deleted or rewritten.
