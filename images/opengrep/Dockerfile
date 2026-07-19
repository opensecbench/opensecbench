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
FROM debian:12-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git \
 && rm -rf /var/lib/apt/lists/*

# The standalone opengrep binary (glibc/manylinux x86-64). Pin to a released version for reproducibility.
ARG OPENGREP_VERSION=v1.25.0
RUN curl -fsSL -o /usr/local/bin/opengrep \
      "https://github.com/opengrep/opengrep/releases/download/${OPENGREP_VERSION}/opengrep_manylinux_x86" \
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
