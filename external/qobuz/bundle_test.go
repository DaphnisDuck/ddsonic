package qobuz

import "testing"

func TestBundlePrivateKeyScraped(t *testing.T) {
	b := &bundle{content: `foo privateKey: "scrapedKey123" bar`}
	if got := b.privateKey(); got != "scrapedKey123" {
		t.Fatalf("privateKey() = %q, want scraped value", got)
	}
}

func TestBundlePrivateKeyFallback(t *testing.T) {
	b := &bundle{content: `no key here at all`}
	if got := b.privateKey(); got != fallbackPrivateKey {
		t.Fatalf("privateKey() = %q, want fallback %q", got, fallbackPrivateKey)
	}
}

func TestBundleAppID(t *testing.T) {
	b := &bundle{content: `x=production:{api:{appId:"798273057",appSecret:"05a4851e74ee47fda346f50cfdfc4f09"}}`}
	got, err := b.appID()
	if err != nil {
		t.Fatalf("appID() error = %v", err)
	}
	if got != "798273057" {
		t.Fatalf("appID() = %q, want 798273057", got)
	}
}
