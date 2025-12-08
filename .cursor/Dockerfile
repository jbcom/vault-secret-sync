# Go development environment for vault-secret-sync
FROM golang:1.25.5-trixie

WORKDIR /workspace

# =============================================================================
# SYSTEM DEPENDENCIES
# =============================================================================
RUN apt-get update && apt-get install -y --no-install-recommends \
    # Version control
    git \
    git-lfs \
    # GitHub CLI
    gh \
    # Task automation
    just \
    && rm -rf /var/lib/apt/lists/*

# =============================================================================
# GOLANGCI-LINT
# =============================================================================
# Install latest golangci-lint for Go 1.25 compatibility
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b /usr/local/bin latest && \
    golangci-lint --version

# =============================================================================
# GIT LFS CONFIGURATION
# =============================================================================
# --skip-smudge prevents workspace crashes with large LFS repos
RUN git lfs install --system --skip-smudge

# Verify installations
RUN go version && golangci-lint --version && gh --version
