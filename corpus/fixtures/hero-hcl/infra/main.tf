# hero-hcl tier-1 fixture: the primary infra file.
region = "eu-central-1"

resource "graphi_service" "core" {
  name = "core"
  size = "small"
}

variable "replicas" {
  default = 2
}
