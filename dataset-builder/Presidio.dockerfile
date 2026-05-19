# Presidio analyzer + anonymizer sidecar for PII redaction.
# Build: docker build -f Presidio.dockerfile -t presidio-sidecar .
# Run:   docker run -d -p 5002:3000 presidio-sidecar
#
# The analyzer runs on port 3000 inside the container; host maps to 5002.
FROM mcr.microsoft.com/presidio-analyzer:2.2
# Presidio analyzer image already includes the service on port 3000.
# No additional layers needed for the basic analysis API.
EXPOSE 3000
