# hero-hcl tier-1 fixture: a second infra file in the SAME directory that
# declares the same block name, which is what makes the qualified name
# infra.resource.graphi_service.core ambiguous.
resource "graphi_service" "core" {
  name = "core-replica"
  size = "small"
}

output "endpoint" {
  value = "https://example.invalid"
}
