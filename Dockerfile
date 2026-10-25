FROM gcr.io/distroless/static-debian12:nonroot

# Binaries are staged as dist/<goarch>/quicssh-punch by the workflow.
ARG TARGETARCH
COPY --chmod=0755 dist/${TARGETARCH}/quicssh-punch /usr/local/bin/quicssh-punch

ENTRYPOINT ["/usr/local/bin/quicssh-punch"]
