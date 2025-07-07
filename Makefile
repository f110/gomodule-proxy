BAZEL = bazel
GO    = $(BAZEL) run @rules_go//go --

.PHONY: update-deps
update-deps:
	$(GO) mod tidy
	$(BAZEL) mod tidy
	$(BAZEL) run //:gazelle
