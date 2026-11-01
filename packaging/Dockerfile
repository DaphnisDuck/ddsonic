# The pinned build environment for ddsonic releases (Linux amd64).
#
# Everything that ends up in the binary comes from here: Go from the image
# digest, the codec libraries from one Debian snapshot, and mpg123 from a
# checksummed source archive. Debian 12 sets the glibc baseline (2.36).
# To move any of them, change the pin here and cut a new candidate.
FROM golang:1.27.3-bookworm@sha256:972b4110ba0f47539a975c31e52c64765e1ff19f2d74469ae4a595c6676e80a5

ARG DEBIAN_SNAPSHOT=20260918T000000Z
ARG MPG123_VERSION=1.32.6
ARG MPG123_SHA256=ccdd1d0abc31d73d8b435fc658c79049d0a905b30669b6a42a03ad169dc609e6

RUN rm -f /etc/apt/sources.list.d/* \
 && printf '%s\n' \
      "Types: deb deb-src" \
      "URIs: http://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}" \
      "Suites: bookworm bookworm-updates" \
      "Components: main" \
      "Check-Valid-Until: no" \
      "Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg" \
      "" \
      "Types: deb deb-src" \
      "URIs: http://snapshot.debian.org/archive/debian-security/${DEBIAN_SNAPSHOT}" \
      "Suites: bookworm-security" \
      "Components: main" \
      "Check-Valid-Until: no" \
      "Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg" \
      > /etc/apt/sources.list.d/snapshot.sources \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
      bzip2 jq pkg-config libasound2-dev libflac-dev libvorbis-dev libogg-dev \
 && rm -rf /var/lib/apt/lists/*

# The sources of the codec libraries the binary links statically, kept in the
# image so every release ships the exact source it was built from.
RUN mkdir -p /native-src && cd /native-src \
 && apt-get update \
 && apt-get source --download-only flac libvorbis libogg \
 && rm -rf /var/lib/apt/lists/* \
 && curl -fsSL "https://www.mpg123.de/download/mpg123-${MPG123_VERSION}.tar.bz2" -o "mpg123-${MPG123_VERSION}.tar.bz2" \
 && echo "${MPG123_SHA256}  mpg123-${MPG123_VERSION}.tar.bz2" | sha256sum -c -

# mpg123 is built from source: Debian's package has no static library.
RUN tar -xjf "/native-src/mpg123-${MPG123_VERSION}.tar.bz2" -C /tmp \
 && cd "/tmp/mpg123-${MPG123_VERSION}" \
 && ./configure --with-cpu=x86-64 --disable-shared --enable-static \
      --disable-components --enable-libmpg123 --disable-network --prefix=/opt/mpg123 \
 && make -j"$(nproc)" && make install \
 && install -Dm644 COPYING /opt/mpg123/COPYING \
 && rm -rf "/tmp/mpg123-${MPG123_VERSION}"

# Without the .so links the linker takes the static archives, so the binary
# does not depend on the codec sonames of the machine it runs on.
RUN rm -f /usr/lib/x86_64-linux-gnu/libFLAC.so \
          /usr/lib/x86_64-linux-gnu/libvorbis.so \
          /usr/lib/x86_64-linux-gnu/libvorbisenc.so \
          /usr/lib/x86_64-linux-gnu/libogg.so

ENV MPG123_VERSION=${MPG123_VERSION} \
    PKG_CONFIG_PATH=/opt/mpg123/lib/pkgconfig:/usr/lib/x86_64-linux-gnu/pkgconfig \
    CGO_ENABLED=1 \
    GOTOOLCHAIN=local \
    GOFLAGS=-buildvcs=false
