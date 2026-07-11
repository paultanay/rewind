# Distroless image — minimal attack surface, no shell.
# The binary is cross-compiled; this stage just packages it.
FROM gcr.io/distroless/static:nonroot

COPY rewind /rewind

USER nonroot:nonroot
ENTRYPOINT ["/rewind"]
