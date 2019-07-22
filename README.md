## osleaktest

Checks for leaked fds, child processes and temp files. Inspired by https://github.com/fortytw2/leaktest

### Usage

Note that `osleaktest` might not work well when running tests in parallel as the tests
run in the same process making it hard to know which resource is used by who.

```go
func Test(t *testing.T) {
    defer osleaktest.Check(t)()
    // test that uses fds, processes or temp files
}
```
