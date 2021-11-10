.PHONY: update-deps
update-deps:
	go mod vendor
	bazel run //:gazelle