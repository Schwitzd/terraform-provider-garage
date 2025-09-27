resource "garage_bucket" "bucket" {}

resource "garage_bucket" "global_example" {
  global_alias = "my-global-bucket"

  quotas {
    max_size    = 1073741824 # 1 GiB
    max_objects = 1000
  }
}
