FROM docker.io/library/golang:1 AS dashboard
WORKDIR /os/dashboard
COPY dashboard/go.mod dashboard/go.sum ./
RUN go mod download
COPY dashboard/ ./
RUN go vet ./... \
  && test -z "$(gofmt -l .)" \
  && go test ./... \
  && CGO_ENABLED=0 go build -trimpath -ldflags=-s -o /dashboard .

FROM quay.io/fedora/fedora-coreos:stable

RUN curl -fsSL -o /etc/yum.repos.d/tailscale.repo \
      https://pkgs.tailscale.com/stable/fedora/tailscale.repo \
  && dnf -y install \
      bootc btop btrfs-progs firewalld \
      git-core glibc-langpack-en jq nano nftables restic rsync \
      samba samba-common-tools \
      smartmontools tailscale \
  && rm /etc/yum.repos.d/tailscale.repo \
  && sed -i 's/^DefaultZone=.*/DefaultZone=FedoraServer/' /etc/firewalld/firewalld.conf \
  && dnf clean all \
  && rm -rf /var/log/dnf5.log /var/cache/libdnf5 /var/lib/dnf /run/dnf \
  && bootc container lint

COPY tree/ /

COPY --from=dashboard /dashboard /usr/bin/dashboard

COPY cloudflare/src/index.js /usr/share/citrine/cloudflare/src/
COPY cloudflare/cloudflare.config.ts /usr/share/citrine/cloudflare/
COPY cloudflare/waf/ /usr/share/citrine/cloudflare/waf/

RUN chmod 0755 /usr/bin/deploy /usr/bin/citrine-app /usr/libexec/citrine/* \
  && chmod 0644 /usr/libexec/citrine/common \
  && chmod 0440 /etc/sudoers.d/citrine

ARG VERSION=dev
ARG COMMIT=unknown
ARG SOURCE=

LABEL org.opencontainers.image.version=$VERSION
LABEL org.opencontainers.image.source=$SOURCE

RUN { echo "VERSION=$VERSION"; echo "COMMIT=$COMMIT"; echo "SOURCE=$SOURCE"; } >/usr/lib/citrine-release

RUN systemctl enable \
      citrine-containment.service \
      citrine-pool.service \
      citrine-pool-heal.service \
      citrine-setup.service \
      citrine-first-boot.service \
      citrine-site.service \
      citrine-site-poll.timer \
      citrine-cloudflare.service \
      citrine-cloudflare.timer \
      citrine-backup.timer \
      citrine-scrub.timer \
      citrine-reboot.timer \
      citrine-signed-ref.service \
      citrine-boot-check.timer \
      firewalld.service \
      rpm-ostreed-automatic.timer \
  && systemctl mask zincati.service \
  && systemctl --global enable citrine-visits.service \
  && bootc container lint
