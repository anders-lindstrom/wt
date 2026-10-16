// Package testkit holds the fixtures and fakes the command packages' tests
// share: repositories to run against, a fake gh, JSON schema checks. Only
// tests import it, directly or through the <pkg>test fixture packages; it
// is never linked into wt.
package testkit
