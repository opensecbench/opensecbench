# OSB image — opengrep (the LGPL-2.1 Semgrep fork) as the default SAST engine with dataflow reachability
# (ADR-0036). opengrep restores taint / inter-procedural analysis and, critically, the SARIF `codeFlows`
# dataflow trace + metavariables that Semgrep moved behind a commercial login — so SAST reachability
# (ADR-0032) works with a fully open tool. Nothing sensitive is baked in.
#
#   Build:      make image-opengrep      (→ osb/opengrep:latest)
#   Pin:        docker build --build-arg OPENGREP_VERSION=vX.Y.Z -t osb/opengrep:vX.Y.Z images/opengrep
#
# The capability invokes `opengrep scan …` directly (no ENTRYPOINT). git is present because opengrep
# enumerates files via git when the target is a repo; ca-certificates for fetching registry rulesets.
FROM debian:13-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git \
 && rm -rf /var/lib/apt/lists/*

# The standalone opengrep binary (glibc/manylinux). Fetch the asset matching the build platform's arch so
# the image builds and runs natively on both x86-64 and arm64 (e.g. Apple Silicon) — no emulation. TARGETARCH
# is set automatically by BuildKit/buildx. Pin to a released version for reproducibility.
ARG OPENGREP_VERSION=v1.25.0
ARG TARGETARCH
RUN case "${TARGETARCH}" in \
      amd64) asset=opengrep_manylinux_x86 ;; \
      arm64) asset=opengrep_manylinux_aarch64 ;; \
      *) echo "unsupported TARGETARCH=${TARGETARCH:-<unset>} (need amd64 or arm64)" >&2; exit 1 ;; \
    esac \
 && curl -fsSL -o /usr/local/bin/opengrep \
      "https://github.com/opengrep/opengrep/releases/download/${OPENGREP_VERSION}/${asset}" \
 && chmod +x /usr/local/bin/opengrep \
 && opengrep --version

LABEL org.opensecbench.image="opengrep" \
      org.opensecbench.adr="ADR-0036"

# opengrep's Python runtime reads rule/source files with the locale encoding; debian-slim has no locale
# (ASCII), so a UTF-8 byte in a ruleset or source file crashes it. Force UTF-8 regardless of locale.
ENV PYTHONUTF8=1 \
    LANG=C.UTF-8 \
    LC_ALL=C.UTF-8

CMD ["opengrep", "--version"]
