# hero-hcl tier-1 fixture: a sibling-directory file. HCL's native
# syntax specification defines no file-inclusion construct, so nothing here
# can name a declaration in infra/ and nothing in infra/ can name this one.
cidr = "10.0.0.0/16"

resource "graphi_network" "subnet" {
  cidr = "10.0.1.0/24"
}
