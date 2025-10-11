# Minimal example binding an access key to a bucket with read/write permissions
resource "garage_bucket" "data" {
  global_alias = "example-bucket"
}

resource "garage_key" "app" {
  name = "application-key"
  permissions {
    read = true
  }
}

resource "garage_bucket_key" "binding" {
  bucket_id     = garage_bucket.data.id
  access_key_id = garage_key.app.access_key_id

  read  = true
  write = true
}
