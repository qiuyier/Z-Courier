# Z-Courier Protocol V1 Fixtures

These files are the language-neutral conformance source for the V1 TCP wire
format.

- `valid.json` contains packet fields plus exact inner-packet and outer-frame
  hex. Large boundary cases use deterministic byte expansion and SHA-256
  values to avoid committing a multi-hundred-kilobyte fixture.
- `invalid.json` derives malformed input from a named valid vector using byte
  mutations. Error names are stable conformance categories rather than
  language-specific exception text.

All byte offsets and lengths are measured in bytes. All hex strings use lower
case characters without a prefix. The `seq` and `timestamp` fields are decimal
strings so JSON parsers cannot silently lose 64-bit precision.

An implementation passes conformance when it produces the valid bytes exactly
and rejects every invalid vector with the corresponding error category.

