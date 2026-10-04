---
scope: package
summary: Signs personal send admission callbacks after plugin mutation and before durable append.
---

# Personal Send Authorization Flow

## Responsibility

Implements `message.PersonalSendAuthorizer` using one private signed HTTP callback per recipient. The app configures URL, HMAC key, timeout and bounded concurrency.

## Boundaries

The adapter reads no cluster or account database. Metadata friendship ACLs still run normally. The usecase checks the callback after plugin mutation and before cluster append. Requests carry only sender/recipient UIDs and trusted system-recipient metadata, never message content or tokens.

## Main Flows

1. A personal SEND admission signs POST, exact path, timestamp, UUID nonce and SHA-256 of the exact JSON body.
2. The callback returns an explicit decision for the current recipient. Successful decisions are never cached; redirects are disabled.
3. Trusted system senders retain security notification paths. Historical messages and sync are unchanged.

## Invariants and Failure Semantics

HTTP failures, malformed responses, missing booleans, cancelled requests and concurrency pressure reject admission. The callback is optional for general deployments; Link-U must enable it.

## Read First

- [Adapter](http.go)

## Update Triggers

Update when callback shape, signing, rejection, concurrency or admission order changes.
