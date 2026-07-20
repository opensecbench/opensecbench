# Assessment-toolchain image for interactive sessions (ADR-0007, review #5).
#
# The terminal opens into a sandboxed container (no network by default, memory/CPU capped). The minimal
# built-in default (alpine:3) has only a shell; build this image and point OSB_SESSION_IMAGE at it to give
# sessions a real toolkit:
#
#   docker build -t osb-session:local pkg/session
#   OSB_SESSION_IMAGE=osb-session:local <run the control plane>
#
# Pin to a digest for a reproducible sandbox in real use.
FROM debian:12-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
      bash ca-certificates curl wget jq git \
      python3 python3-pip python3-venv \
      openssh-client dnsutils iputils-ping net-tools \
      ripgrep less vim-tiny \
    && rm -rf /var/lib/apt/lists/*

# Cloud CLIs are large and optional — uncomment what an engagement needs:
#   awscli:   RUN pip3 install --no-cache-dir --break-system-packages awscli
#   azure:    RUN curl -sL https://aka.ms/InstallAzureCLIDeb | bash
#   gcloud:   see https://cloud.google.com/sdk/docs/install

# Run as a non-root user inside the already-sandboxed container.
RUN useradd -m -s /bin/bash analyst
USER analyst
WORKDIR /home/analyst

CMD ["sleep", "infinity"]
