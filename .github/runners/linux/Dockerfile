# Why: CI needs a Linux machine with Go and LLVM 21, and workflow code should
# run apart from the host that serves it.
#
# What: an Ubuntu 24.04 image with Go, LLVM 21 (clang, lld, llvm tools) and
# the GitHub Actions runner, running as an unprivileged user, with a
# job-started hook (job-started.sh) that fails every job but the nightly
# workflow of REPO on main.
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
# The repository's signing key is checked against its fingerprint first.
ARG LLVM_APT_KEY=6084F3CF814B57C1CF12EFD515CF4D18AF4F7421
RUN curl -fsSL -o /tmp/llvm.asc https://apt.llvm.org/llvm-snapshot.gpg.key \
    && fpr=$(gpg --show-keys --with-colons /tmp/llvm.asc | awk -F: '$1 == "fpr" { print $10; exit }') \
    && { [ "$fpr" = "$LLVM_APT_KEY" ] || { echo "apt.llvm.org's key has the fingerprint '$fpr', not $LLVM_APT_KEY" >&2; exit 1; }; } \
    && gpg --dearmor -o /usr/share/keyrings/llvm.gpg /tmp/llvm.asc \
    && rm /tmp/llvm.asc \
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

# The entrypoint and the job-started hook belong to root, in a directory
# the runner's user cannot write, so no job can change them.
ARG REPO=zerodayokay/Mk-Lang
COPY entrypoint.sh job-started.sh /usr/local/lib/mk-runner/
RUN chown -R root:root /usr/local/lib/mk-runner \
    && chmod 755 /usr/local/lib/mk-runner /usr/local/lib/mk-runner/entrypoint.sh /usr/local/lib/mk-runner/job-started.sh
ENV MK_RUNNER_REPO=${REPO} \
    ACTIONS_RUNNER_HOOK_JOB_STARTED=/usr/local/lib/mk-runner/job-started.sh

USER runner
ENTRYPOINT ["/bin/bash", "/usr/local/lib/mk-runner/entrypoint.sh"]
