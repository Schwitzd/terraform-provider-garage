# Create a bucket
resource "garage_bucket" "data" {
  global_alias = "main-data"
}

# Add another global alias pointing to the same bucket
resource "garage_bucket_alias" "alt_global" {
  bucket_id    = garage_bucket.data.id
  global_alias = "secondary-data"
}