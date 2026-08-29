# Cross-repository golden fixture

`geosite.json` is generated from an in-memory protobuf fixture by
`TestDatToJSONMatchesForgeRuleCoreGoldenFixture`. It contains only IANA-reserved
`.test` names and a synthetic keyword; it has no user domains, endpoints,
credentials, or downloaded source data.

The byte-identical fixture is consumed by ForgeRuleCore at:

`ForgeRuleCore/Tests/ForgeRuleCoreTests/Fixtures/Bundle/geosite-valid.json`

Expected SHA-256:

`0f224dfb0a0ef20db3033aeb588523c733518d6c82d06f8f393bc967e1d212e3`
