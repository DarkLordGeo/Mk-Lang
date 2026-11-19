# Why: CI needs a Linux machine with Go and LLVM 21, and workflow code should
# run apart from the host that serves it.
#
# What: an Ubuntu 24.04 image with Go, LLVM 21 (clang, lld, llvm tools) and
# the GitHub Actions runner, running as an unprivileged user.
#
# How: the Go and runner downloads are pinned and checked against their
# SHA-256; the base image and LLVM come from the ubuntu:24.04 tag and LLVM's
# apt repository as they stand at build time. The runner
# registers once (start.ps1 runs config.sh in a one-off container) and keeps
# its registration in the mk-runner-linux volume, so later starts need no
# token.
FROM ubuntu:24.04

ARG RUNNER_VERSION=2.338.2
ARG RUNNER_SHA256=baebf204307bac6193480852732f0aba2ae97b6c5597c244f4d5558390147c40
ARG GO_VERSION=1.27.3
ARG GO_SHA256=65efbc80fdc1fa2cd2631cad5b481777a68e8725f34e89f50fc6ac714e379b40
ARG LLVM_VERSION=21

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       ca-certificates curl git gnupg jq lsb-release python3 tar unzip xz-utils zstd \
    && rm -rf /var/lib/apt/lists/*

# LLVM from apt.llvm.org: mk drives clang and lld from /usr/lib/llvm-21/bin.
RUN curl -fsSL https://apt.llvm.org/llvm-snapshot.gpg.key | gpg --dearmor -o /usr/share/keyrings/llvm.gpg \
    && echo "deb [signed-by=/usr/share/keyrings/llvm.gpg] http://apt.llvm.org/noble/ llvm-toolchain-noble-${LLVM_VERSION} main" \
       > /etc/apt/sources.list.d/llvm.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends clang-${LLVM_VERSION} lld-${LLVM_VERSION} llvm-${LLVM_VERSION} \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL -o /tmp/go.tgz https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz \
    && echo "${GO_SHA256}  /tmp/go.tgz" | sha256sum -c - \
    && tar -C /usr/local -xzf /tmp/go.tgz \
    && rm /tmp/go.tgz

ENV PATH=/usr/local/go/bin:/usr/lib/llvm-${LLVM_VERSION}/bin:$PATH \
    GOCACHE=/work/.gocache \
    GOPATH=/work/.gopath

RUN useradd -m -s /bin/bash runner \
    && mkdir -p /work /home/runner/actions-runner

WORKDIR /home/runner/actions-runner
RUN curl -fsSL -o /tmp/runner.tgz https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-x64-${RUNNER_VERSION}.tar.gz \
    && echo "${RUNNER_SHA256}  /tmp/runner.tgz" | sha256sum -c - \
    && tar -xzf /tmp/runner.tgz \
    && rm /tmp/runner.tgz \
    && ./bin/installdependencies.sh \
    && rm -rf /var/lib/apt/lists/* \
    && chown -R runner:runner /home/runner /work

COPY --chown=runner:runner entrypoint.sh /home/runner/entrypoint.sh

USER runner
ENTRYPOINT ["/bin/bash", "/home/runner/entrypoint.sh"]
