resource "garage_key" "app_key" {
  name = "mykey"
  permissions {
    read  = true
    write = true
  }
}