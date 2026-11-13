#!/usr/bin/env python3
"""Local release failure/recovery scenarios with mutation and identity oracles.

All child processes use fixed argument lists and shell=False. GitHub and curl
resolve only to the copied local fixtures; neither tokens nor network access
are needed. API plans model explicit external writers and transport responses,
not production decisions. Every scenario checks the exact exit status, ordered
DELETE/PATCH attempts (including failed requests), and both remaining objects.
"""

import argparse
from dataclasses import dataclass, field
import hashlib
import json
import os
from pathlib import Path
import shutil
# Process trust boundaries are audited at the argv-only calls below.
import subprocess  # nosec B404
import sys
import tempfile


ROOT = Path(__file__).resolve().parent.parent
SCRIPTS = ROOT / "Scripts"
REPO = "CoderQuinn/ForgeRules"
PREFIX = "repos/" + REPO
SHA = "a" * 40
OTHER_SHA = "b" * 40
TAG_SHA = "c" * 40
DATED = "rules-20260830"
STAGING = "latest-staging-123-1"
EXACT = PREFIX + "/releases/101"
LIST = PREFIX + "/releases?per_page=100"
ASSETS = (
    "loyalsoldier_geoip.mmdb", "loyalsoldier_geosite.json",
    "official_geoip.mmdb", "official_geosite.json",
    "rules-manifest.json", "rules.sources.lock.json",
)
PRESERVE = object()


def release(tag=DATED, draft=False, **changes):
    value = {
        "id": 101, "node_id": "R_101", "tag_name": tag,
        "name": "Fixture Release", "draft": draft, "prerelease": False,
        "immutable": False, "target_commitish": SHA,
        "updated_at": "2026-08-30T00:00:00Z",
        "make_latest": "true" if tag == "latest" and not draft else "false",
    }
    value.update(changes)
    return value


def ref(tag=DATED, **changes):
    value = {"name": tag, "type": "commit", "sha": SHA}
    value.update(changes)
    return value


def by_tag(tag):
    return PREFIX + "/releases/tags/" + tag


def tag_ref(tag):
    return PREFIX + "/git/ref/tags/" + tag


def delete_ref(tag):
    return "DELETE " + PREFIX + "/git/refs/tags/" + tag


DELETE = "DELETE " + EXACT
PATCH = "PATCH " + EXACT


def action(endpoint, on=1, method="GET", **fields):
    return dict(method=method, endpoint=endpoint, on=on, **fields)


def error(endpoint, on=1, method="GET", **changes):
    return action(endpoint, on, method, status=1,
                  error="gh: injected transport error (HTTP 503)", **changes)


@dataclass
class Case:
    name: str
    script: str
    args: list
    initial_release: object = None
    initial_tag: object = None
    plan: list = field(default_factory=list)
    curl_plan: list = field(default_factory=list)
    environment: dict = field(default_factory=dict)
    exit_status: int = 1
    mutations: tuple = ()
    final_release: object = PRESERVE
    final_tag: object = PRESERVE
    diagnostic: str = ""
    missing_asset: str = ""
    annotated: object = None
    github_output: str = ""
    curl_requests: tuple = ()


def latest(name, **fields):
    return Case("latest/" + name, "delete-latest-release.sh", [REPO],
                initial_release=release("latest"), initial_tag=ref("latest"),
                **fields)


def cleanup(name, tag=DATED, draft=False, **fields):
    return Case("cleanup/" + name, "cleanup-failed-draft-release.sh",
                [REPO, "101", tag, SHA],
                initial_release=fields.pop("initial_release", release(tag, draft)),
                initial_tag=fields.pop("initial_tag", None if draft else ref(tag)), **fields)


def patch_case(name, retag=False, **fields):
    tag = STAGING if retag else DATED
    args = [REPO, "101", tag, "latest", SHA] if retag else [REPO, "101", tag, SHA]
    script = "retag-draft-release.sh" if retag else "publish-draft-release.sh"
    return Case(("retag/" if retag else "publish/") + name, script, args,
                initial_release=release(tag, True), **fields)


def validation_cases():
    scripts = {
        "create-draft-release.sh": [REPO, DATED, "Fixture", SHA, "{artifacts}"],
        "publish-draft-release.sh": [REPO, "101", DATED, SHA],
        "retag-draft-release.sh": [REPO, "101", STAGING, "latest", SHA],
        "cleanup-failed-draft-release.sh": [REPO, "101", DATED, SHA],
        "delete-latest-release.sh": [REPO],
        "verify-release-assets.sh": [REPO, "101", DATED, SHA, "draft", "{artifacts}"],
        "check-release-tag-available.sh": ["{missing}", DATED],
    }
    for script in scripts:
        yield Case("validation/usage-" + script, script, [], exit_status=64,
                   diagnostic="usage:")
    for field_name, index, value in (
        ("repository", 0, "owner/repo/extra"), ("id", 1, "not-a-number"),
        ("tag", 2, "rules-invalid"), ("sha", 3, "A" * 40),
    ):
        args = list(scripts["publish-draft-release.sh"])
        args[index] = value
        yield Case("validation/" + field_name, "publish-draft-release.sh", args,
                   exit_status=64)
    extras = (
        ("publish-staging", "publish-draft-release.sh", [REPO, "101", STAGING, SHA]),
        ("retag-direction", "retag-draft-release.sh", [REPO, "101", DATED, "latest", SHA]),
        ("retag-destination", "retag-draft-release.sh", [REPO, "101", STAGING, DATED, SHA]),
        ("create-empty-name", "create-draft-release.sh", [REPO, DATED, "", SHA, "{artifacts}"]),
        ("create-missing-directory", "create-draft-release.sh", [REPO, DATED, "Fixture", SHA, "{missing}"]),
        ("verify-state", "verify-release-assets.sh", [REPO, "101", DATED, SHA, "public", "{artifacts}"]),
        ("verify-missing-directory", "verify-release-assets.sh", [REPO, "101", DATED, SHA, "draft", "{missing}"]),
        ("tag-remote-empty", "check-release-tag-available.sh", ["", DATED]),
        ("tag-date-format", "check-release-tag-available.sh", ["{missing}", "latest"]),
    )
    for name, script, args in extras:
        yield Case("validation/" + name, script, args, exit_status=64)
    create_args = scripts["create-draft-release.sh"]
    for name, environment in (
        ("missing-token", {"GH_TOKEN": ""}),
        ("missing-output", {"GITHUB_OUTPUT": ""}),
        ("unwritable-output", {"GITHUB_OUTPUT": "{root}"}),
    ):
        yield Case("validation/create-" + name, "create-draft-release.sh",
                   create_args, environment=environment, exit_status=64)
    yield Case("validation/create-missing-asset", "create-draft-release.sh",
               create_args, missing_asset=ASSETS[0], diagnostic="release asset does not exist")
    yield Case("validation/tag-local-query-failure", "check-release-tag-available.sh",
               ["{missing}", DATED], diagnostic="unable to query release tag")


def latest_cases():
    # An absent namespace must remain absent through all three rechecks.
    for name, fault in (
        ("absence-list-race", action(LIST, 2, mutate_release=release("latest"))),
        ("absence-release-race", action(by_tag("latest"), 2, mutate_release=release("latest"))),
        ("absence-tag-race", action(tag_ref("latest"), 2, mutate_tag=ref("latest"))),
        ("absence-release-query-error", error(by_tag("latest"), 2)),
        ("absence-ref-query-error", error(tag_ref("latest"), 2)),
    ):
        yield Case("latest/" + name, "delete-latest-release.sh", [REPO], plan=[fault],
                   final_release=fault.get("mutate_release"), final_tag=fault.get("mutate_tag"))
    yield latest("initial-ref-auth-error", plan=[action(tag_ref("latest"), status=4,
                 error="gh: authentication required")])
    yield latest("malformed-list", plan=[action(LIST, response={"bad": "pages"})],
                 diagnostic="release listing is not a paginated array")
    for field_name, value in (("id", "invalid"), ("tag_name", DATED),
                              ("draft", True), ("immutable", True)):
        snapshot = release("latest", **{field_name: value})
        yield latest("snapshot-" + field_name,
                     plan=[action(by_tag("latest"), response=snapshot)],
                     exit_status=64 if field_name == "id" else 1)
    yield latest("snapshot-list-disagrees", plan=[action(LIST, response=[[release("latest", id=202)]])])
    yield latest("listing-recheck-changed", plan=[action(LIST, 2, response=[[]])])
    yield latest("release-recheck-disappeared", plan=[action(by_tag("latest"), 2, remove_release=True)],
                 final_release=None)
    yield latest("release-recheck-error", plan=[error(by_tag("latest"), 2)])
    yield latest("release-recheck-malformed", plan=[action(by_tag("latest"), 2, response=release("latest", name=[]))])
    yield latest("release-recheck-changed", plan=[action(by_tag("latest"), 2, mutate_release={"name": "external"})],
                 final_release=release("latest", name="external"))
    yield latest("ref-recheck-disappeared", plan=[action(tag_ref("latest"), 2, remove_tag=True)], final_tag=None)
    yield latest("ref-recheck-error", plan=[error(tag_ref("latest"), 2)])
    # A retry is permitted exactly once, with the complete original pair intact.
    yield latest("release-delete-exhausted", plan=[error(EXACT, 1, "DELETE"), error(EXACT, 2, "DELETE")],
                 mutations=(DELETE, DELETE), diagnostic="survived one safe delete retry")
    yield latest("release-delete-changed-survivor", plan=[error(EXACT, method="DELETE", mutate_release={"name": "external"})],
                 mutations=(DELETE,), final_release=release("latest", name="external"))
    yield latest("release-delete-malformed-survivor", plan=[error(EXACT, method="DELETE", mutate_release={"name": []})],
                 mutations=(DELETE,), final_release=release("latest", name=[]))
    yield latest("release-delete-query-error", plan=[error(EXACT)], mutations=(DELETE,), final_release=None)
    yield latest("release-retry-list-changed", plan=[error(EXACT, method="DELETE"), action(LIST, 3, response=[[]])],
                 mutations=(DELETE,))
    yield latest("release-retry-ref-missing", plan=[error(EXACT, method="DELETE"), action(tag_ref("latest"), 3, remove_tag=True)],
                 mutations=(DELETE,), final_tag=None)
    yield latest("release-retry-ref-changed", plan=[error(EXACT, method="DELETE"), action(tag_ref("latest"), 3, mutate_tag={"sha": OTHER_SHA})],
                 mutations=(DELETE,), final_tag=ref("latest", sha=OTHER_SHA))
    yield latest("release-after-delete-list-race", plan=[action(LIST, 3, mutate_release=release("latest", id=202, node_id="R_202"))],
                 mutations=(DELETE,), final_release=release("latest", id=202, node_id="R_202"))
    yield latest("release-after-delete-by-tag-race", plan=[action(by_tag("latest"), 3, mutate_release=release("latest", id=202, node_id="R_202"))],
                 mutations=(DELETE,), final_release=release("latest", id=202, node_id="R_202"))
    yield latest("release-after-delete-by-tag-error", plan=[error(by_tag("latest"), 3)], mutations=(DELETE,), final_release=None)
    yield latest("release-after-delete-ref-absent", plan=[action(tag_ref("latest"), 3, remove_tag=True)],
                 mutations=(DELETE,), final_release=None, final_tag=None, exit_status=0)
    yield latest("release-after-delete-ref-error", plan=[error(tag_ref("latest"), 3)], mutations=(DELETE,), final_release=None)
    yield latest("tag-delete-exhausted", plan=[error(PREFIX + "/git/refs/tags/latest", 1, "DELETE"), error(PREFIX + "/git/refs/tags/latest", 2, "DELETE")],
                 mutations=(DELETE, delete_ref("latest"), delete_ref("latest")), final_release=None,
                 diagnostic="tag survived one safe delete retry")
    yield latest("tag-delete-changed-survivor", plan=[error(PREFIX + "/git/refs/tags/latest", method="DELETE", mutate_tag={"sha": OTHER_SHA})],
                 mutations=(DELETE, delete_ref("latest")), final_release=None, final_tag=ref("latest", sha=OTHER_SHA))
    yield latest("tag-delete-query-error", plan=[error(tag_ref("latest"), 4)],
                 mutations=(DELETE, delete_ref("latest")), final_release=None, final_tag=None)
    yield latest("tag-retry-list-race", plan=[error(PREFIX + "/git/refs/tags/latest", method="DELETE"), action(LIST, 4, mutate_release=release("latest", id=202, node_id="R_202"))],
                 mutations=(DELETE, delete_ref("latest")), final_release=release("latest", id=202, node_id="R_202"))
    yield latest("final-list-race", plan=[action(LIST, 4, mutate_release=release("latest", id=202, node_id="R_202"))],
                 mutations=(DELETE, delete_ref("latest")), final_release=release("latest", id=202, node_id="R_202"), final_tag=None)
    yield latest("final-ref-race", plan=[action(tag_ref("latest"), 5, mutate_tag=ref("latest", sha=OTHER_SHA))],
                 mutations=(DELETE, delete_ref("latest")), final_release=None, final_tag=ref("latest", sha=OTHER_SHA))
    yield latest("final-ref-query-error", plan=[error(tag_ref("latest"), 5)],
                 mutations=(DELETE, delete_ref("latest")), final_release=None, final_tag=None)


def cleanup_cases():
    yield Case("cleanup/absent-id-release-remnant", "cleanup-failed-draft-release.sh",
               [REPO, "101", DATED, SHA], initial_release=release(id=202, node_id="R_202"))
    yield Case("cleanup/absent-id-ref-query-error", "cleanup-failed-draft-release.sh",
               [REPO, "101", DATED, SHA], plan=[error(tag_ref(DATED))])
    for name, override in (("wrong-id", {"id": 202}), ("unowned-tag", {"tag_name": "latest"}),
                            ("wrong-target", {"target_commitish": OTHER_SHA}), ("bad-fingerprint", {"name": []})):
        yield cleanup(name, draft=True, plan=[action(EXACT, response=release(DATED, True, **override))])
    yield cleanup("draft-release-recheck-error", draft=True, plan=[error(EXACT, 2)])
    yield cleanup("draft-release-recheck-absent", draft=True, plan=[action(EXACT, 2, remove_release=True)], final_release=None)
    yield cleanup("draft-release-recheck-malformed", draft=True, plan=[action(EXACT, 2, response=release(DATED, True, name=[]))])
    yield cleanup("draft-release-recheck-changed", draft=True, plan=[action(EXACT, 2, mutate_release={"updated_at": "external"})],
                  final_release=release(DATED, True, updated_at="external"))
    # An uncertain public association permits withdrawing the exact owned ID;
    # the unproven ref survives and cleanup reports failure.
    for name, fault in (
        ("listing-error", error(LIST)),
        ("listing-ambiguous", action(LIST, response=[[release(), release(id=202)]])),
        ("by-tag-other-id", action(by_tag(DATED), response=release(id=202))),
        ("by-tag-absent", action(by_tag(DATED), status=1, error="gh: Not Found (HTTP 404)")),
        ("by-tag-error", error(by_tag(DATED))),
        ("listing-recheck-error", error(LIST, 2)),
        ("listing-recheck-other-id", action(LIST, 2, response=[[release(id=202)]])),
        ("by-tag-recheck-other-id", action(by_tag(DATED), 2, response=release(id=202))),
        ("by-tag-recheck-absent", action(by_tag(DATED), 2, status=1, error="gh: Not Found (HTTP 404)")),
        ("by-tag-recheck-error", error(by_tag(DATED), 2)),
    ):
        yield cleanup(name, plan=[fault], mutations=(DELETE,), final_release=None,
                      diagnostic="lacked a stable exact-ID association")
    yield cleanup("reservation-initial-ref-error", plan=[error(tag_ref(DATED))])
    yield cleanup("reservation-initial-absent", plan=[action(tag_ref(DATED), remove_tag=True)], final_tag=None)
    yield cleanup("reservation-recheck-ref-error", plan=[error(tag_ref(DATED), 2)])
    yield cleanup("reservation-recheck-absent", plan=[action(tag_ref(DATED), 2, remove_tag=True)], final_tag=None)
    yield cleanup("reservation-recheck-changed", plan=[action(tag_ref(DATED), 2, mutate_tag={"sha": OTHER_SHA})],
                  mutations=(DELETE,), final_release=None, final_tag=ref(sha=OTHER_SHA))
    yield cleanup("reservation-post-withdrawal-absent", plan=[action(tag_ref(DATED), 3, remove_tag=True)],
                  mutations=(DELETE,), final_release=None, final_tag=None)
    yield cleanup("reservation-post-withdrawal-ref-error", plan=[error(tag_ref(DATED), 3)],
                  mutations=(DELETE,), final_release=None)
    yield cleanup("reservation-post-withdrawal-bad-fingerprint", plan=[action(tag_ref(DATED), 3, response={"object": {"type": "blob", "sha": SHA}})],
                  mutations=(DELETE,), final_release=None)
    yield cleanup("reservation-post-withdrawal-peel-error", initial_tag=ref(type="tag", sha=TAG_SHA),
                  annotated={"tag_sha": TAG_SHA, "object": {"type": "commit", "sha": SHA}},
                  plan=[error(PREFIX + "/git/tags/" + TAG_SHA, 3)], mutations=(DELETE,), final_release=None)
    yield cleanup("draft-delete-failed-before-mutation", draft=True, plan=[error(EXACT, method="DELETE")],
                  mutations=(DELETE,), diagnostic="unable to confirm deletion")
    yield cleanup("draft-delete-success-no-effect", draft=True, plan=[action(EXACT, method="DELETE", status=0)],
                  mutations=(DELETE,), diagnostic="still exists after deletion")
    yield cleanup("draft-delete-confirm-error", draft=True, plan=[error(EXACT, 3)], mutations=(DELETE,), final_release=None)
    yield cleanup("draft-post-delete-list-race", draft=True, plan=[action(LIST, mutate_release=release(DATED, True, id=202, node_id="R_202"))],
                  mutations=(DELETE,), final_release=release(DATED, True, id=202, node_id="R_202"))
    yield cleanup("draft-post-delete-ref-error", draft=True, plan=[error(tag_ref(DATED))], mutations=(DELETE,), final_release=None)
    yield cleanup("latest-ref-recheck-disappeared", tag="latest", plan=[action(tag_ref("latest"), 2, remove_tag=True)],
                  mutations=(DELETE,), final_release=None, final_tag=None)
    yield cleanup("latest-ref-post-withdrawal-absent", tag="latest", plan=[action(tag_ref("latest"), 3, remove_tag=True)],
                  mutations=(DELETE,), final_release=None, final_tag=None, exit_status=0)
    yield cleanup("latest-tag-delete-failed", tag="latest", plan=[error(PREFIX + "/git/refs/tags/latest", method="DELETE")],
                  mutations=(DELETE, delete_ref("latest")), final_release=None, diagnostic="unable to confirm deletion of exact tag")
    yield cleanup("latest-tag-delete-success-no-effect", tag="latest", plan=[action(PREFIX + "/git/refs/tags/latest", method="DELETE", status=0)],
                  mutations=(DELETE, delete_ref("latest")), final_release=None, diagnostic="still exists after deletion")
    yield cleanup("latest-tag-confirm-query-error", tag="latest", plan=[error(tag_ref("latest"), 4)],
                  mutations=(DELETE, delete_ref("latest")), final_release=None, final_tag=None)
    # A published latest release with no ref is permitted; a dated reservation
    # with no ref is rejected above. A new ref must always be preserved.
    for name, fault, status, final_tag in (
        ("already-absent", None, 0, None),
        ("new-ref-race", action(tag_ref("latest"), 3, mutate_tag=ref("latest", sha=OTHER_SHA)), 1, ref("latest", sha=OTHER_SHA)),
        ("new-ref-query-error", error(tag_ref("latest"), 3), 1, None),
    ):
        yield Case("cleanup/latest-ref-" + name, "cleanup-failed-draft-release.sh",
                   [REPO, "101", "latest", SHA], initial_release=release("latest"),
                   plan=[] if fault is None else [fault], mutations=(DELETE,), final_release=None,
                   final_tag=final_tag, exit_status=status)


def patch_cases():
    for retag in (False, True):
        tag = STAGING if retag else DATED
        for name, fault in (
            ("private-list-empty", action(LIST, response=[[]])),
            ("private-by-tag-error", error(by_tag(tag))),
            ("private-by-tag-collision", action(by_tag(tag), response=release(tag))),
            ("private-ref-error", error(tag_ref(tag))),
            ("private-ref-collision", action(tag_ref(tag), mutate_tag=ref(tag))),
        ):
            yield patch_case(name, retag, plan=[fault], final_tag=fault.get("mutate_tag"))
        for name, statuses in (("failed-twice", (1, 1)), ("success-without-effect-twice", (0, 0))):
            yield patch_case(name, retag, plan=[action(EXACT, n + 1, "PATCH", status=status) for n, status in enumerate(statuses)],
                             mutations=(PATCH, PATCH), diagnostic="did not apply after one safe retry")
        yield patch_case("unexpected-state-after-patch", retag,
                         plan=[action(EXACT, method="PATCH", status=1, mutate_release={"target_commitish": OTHER_SHA})],
                         mutations=(PATCH,), final_release=release(tag, True, target_commitish=OTHER_SHA), diagnostic="unexpected state")
        yield patch_case("malformed-reconciliation", retag,
                         plan=[action(EXACT, method="PATCH", status=1), action(EXACT, 2, raw="{broken")],
                         mutations=(PATCH,), diagnostic="unexpected state")
        yield patch_case("reconciliation-query-error", retag,
                         plan=[action(EXACT, method="PATCH", status=1), error(EXACT, 2)], mutations=(PATCH,))
        yield patch_case("retry-private-list-changed", retag,
                         plan=[action(EXACT, method="PATCH", status=1), action(LIST, 3 if retag else 2, response=[[]])], mutations=(PATCH,))
        yield patch_case("retry-private-ref-appeared", retag,
                         plan=[action(EXACT, method="PATCH", status=1), action(tag_ref(tag), 2, mutate_tag=ref(tag))],
                         mutations=(PATCH,), final_tag=ref(tag))
    for name, fault in (
        ("destination-list-collision", action(LIST, 2, response=[[release("latest", True, id=202)]])),
        ("destination-release-collision", action(by_tag("latest"), response=release("latest"))),
        ("destination-ref-collision", action(tag_ref("latest"), mutate_tag=ref("latest"))),
        ("destination-release-error", error(by_tag("latest"))),
        ("destination-ref-error", error(tag_ref("latest"))),
    ):
        yield patch_case(name, True, plan=[fault], final_tag=fault.get("mutate_tag"))
    yield patch_case("retry-destination-list-collision", True,
                     plan=[action(EXACT, method="PATCH", status=1), action(LIST, 4, response=[[release("latest", True, id=202)]])],
                     mutations=(PATCH,))
    published = release(DATED, False)
    for name, fault in (
        ("published-list-empty", action(LIST, 2, response=[[]])),
        ("published-by-tag-id-changed", action(by_tag(DATED), 2, response=release(id=202))),
        ("published-ref-commit-changed", action(tag_ref(DATED), 2, mutate_tag={"sha": OTHER_SHA})),
    ):
        yield patch_case(name, plan=[fault], mutations=(PATCH,), final_release=published,
                         final_tag=ref(sha=OTHER_SHA) if name.endswith("commit-changed") else ref())
    retagged = release("latest", True)
    for name, fault in (
        ("retagged-list-empty", action(LIST, 3, response=[[]])),
        ("retagged-public-release-appeared", action(by_tag("latest"), 2, response=release("latest"))),
        ("retagged-ref-appeared", action(tag_ref("latest"), 2, mutate_tag=ref("latest"))),
        ("staging-list-survived", action(LIST, 4, response=[[release(STAGING, True, id=202)]])),
        ("staging-ref-survived", action(tag_ref(STAGING), 2, mutate_tag=ref(STAGING))),
    ):
        yield patch_case(name, True, plan=[fault], mutations=(PATCH,), final_release=retagged,
                         final_tag=fault.get("mutate_tag"))


def verifier_cases():
    args = [REPO, "101", DATED, SHA, "draft", "{artifacts}"]
    for name, metadata in (("metadata-id", release(DATED, True, id=202)),
                           ("metadata-tag", release("latest", True)),
                           ("metadata-draft", release()),
                           ("metadata-target", release(DATED, True, target_commitish=OTHER_SHA))):
        yield Case("verify/" + name, "verify-release-assets.sh", args,
                   initial_release=release(DATED, True), plan=[action(EXACT, response=metadata)])
    asset_list = [{"id": 1000 + n, "name": name} for n, name in enumerate(sorted(("SHA256SUMS",) + ASSETS))]
    asset_list[0]["id"] = "not-numeric"
    yield Case("verify/non-numeric-asset-id", "verify-release-assets.sh", args,
               initial_release=release(DATED, True), plan=[action(EXACT + "/assets?per_page=100", response=asset_list)],
               diagnostic="no numeric API ID")
    yield Case("verify/download-api-error", "verify-release-assets.sh", args,
               initial_release=release(DATED, True), plan=[error(PREFIX + "/releases/assets/1000")])
    for name, endpoint, payload in (
        ("ref-unsupported-object", tag_ref(DATED), {"object": {"type": "blob", "sha": SHA}}),
        ("ref-malformed-object", tag_ref(DATED), []),
        ("annotated-malformed-target", PREFIX + "/git/tags/" + TAG_SHA, []),
        ("annotated-unsupported-target", PREFIX + "/git/tags/" + TAG_SHA, {"object": {"type": "tree", "sha": SHA}}),
    ):
        yield Case("verify/" + name, "verify-release-assets.sh", [REPO, "101", DATED, SHA, "published", "{artifacts}"],
                   initial_release=release(), initial_tag=ref(type="tag", sha=TAG_SHA),
                   annotated={"tag_sha": TAG_SHA, "object": {"type": "commit", "sha": SHA}},
                   plan=[action(endpoint, response=payload)])
    yield Case("verify/annotated-depth-exhaustion", "verify-release-assets.sh", [REPO, "101", DATED, SHA, "published", "{artifacts}"],
               initial_release=release(), initial_tag=ref(type="tag", sha=TAG_SHA),
               annotated={"tag_sha": TAG_SHA, "object": {"type": "tag", "sha": TAG_SHA}}, diagnostic="exceeds 16 objects")


def creator_cases():
    args = [REPO, DATED, "Fixture", SHA, "{artifacts}"]
    create_url = "https://api.github.com/repos/" + REPO + "/releases"
    create_post = "POST " + create_url
    first_upload = "https://uploads.github.com/repos/" + REPO + "/releases/101/assets?name=SHA256SUMS"
    owned = release(DATED, True)
    for name, fields, status in (
        ("http-failure", {"status": 503, "response": {"message": "unavailable"}}, 1),
        ("transport-failure", {"exit": 7, "error": "fixture curl transport failure"}, 7),
        ("malformed-response", {"raw": "{broken"}, 5),
        ("response-no-id", {"response": {}}, 4),
        ("response-string-id", {"response": {"id": "101"}}, 4),
        ("response-negative-id", {"response": {"id": -1}}, 64),
    ):
        yield Case("create/" + name, "create-draft-release.sh", args,
                   curl_plan=[action(create_url, method="POST", **fields)],
                   exit_status=status, curl_requests=(create_post,))
    # Without a valid fresh ID the workflow cannot guess ownership, even if a
    # server created an object before returning incomplete metadata.
    yield Case("create/missing-id-preserves-unproven-release", "create-draft-release.sh", args,
               curl_plan=[action(create_url, method="POST", mutate_release=owned, response={})],
               exit_status=4, final_release=owned, curl_requests=(create_post,))
    yield Case("create/wrong-response-identity-cleans-owned-id", "create-draft-release.sh", args,
               curl_plan=[action(create_url, method="POST", mutate_release=owned, response=release("latest", True))],
               mutations=(DELETE,), github_output="id=101\n", curl_requests=(create_post,),
               diagnostic="create response does not identify the requested draft")
    changed = release(DATED, True, target_commitish=OTHER_SHA)
    yield Case("create/cleanup-refuses-changed-owned-id", "create-draft-release.sh", args,
               curl_plan=[action(create_url, method="POST", mutate_release=changed, response=release("latest", True))],
               final_release=changed, github_output="id=101\n", curl_requests=(create_post,),
               diagnostic="automatic cleanup refused or failed")
    yield Case("create/fresh-metadata-mismatch-cleans-owned-id", "create-draft-release.sh", args,
               curl_plan=[action(create_url, method="POST", mutate_release=owned, response=owned)],
               plan=[action(EXACT, response=changed)], mutations=(DELETE,),
               github_output="id=101\n", curl_requests=(create_post,))
    for name, fields in (
        ("upload-http-failure", {"status": 500, "response": {"message": "upload unavailable"}}),
        ("upload-response-wrong-name", {"response": {"id": 1001, "name": "wrong", "state": "uploaded"}}),
        ("upload-response-wrong-state", {"response": {"id": 1001, "name": "SHA256SUMS", "state": "new"}}),
        ("upload-response-wrong-id", {"response": {"id": "bad", "name": "SHA256SUMS", "state": "uploaded"}}),
    ):
        yield Case("create/" + name, "create-draft-release.sh", args,
                   curl_plan=[action(first_upload, method="POST", **fields)], mutations=(DELETE,),
                   github_output="id=101\n", curl_requests=(create_post, "POST " + first_upload))
    yield Case("create/preexisting-tag-ref", "create-draft-release.sh", args, initial_tag=ref(),
               diagnostic="exact tag ref already exists")
    yield Case("create/ref-lookup-authentication-error", "create-draft-release.sh", args,
               plan=[action(tag_ref(DATED), status=4, error="gh: authentication required")])


def run_case(case, base):
    directory = base / case.name.replace("/", "-")
    directory.mkdir()
    state = directory / "state"
    artifacts = directory / "artifacts"
    state.mkdir()
    artifacts.mkdir()
    for name in ASSETS:
        (artifacts / name).write_bytes(("fixture:" + name + "\n").encode())
    sums = "".join(hashlib.sha256((artifacts / name).read_bytes()).hexdigest() + "  " + name + "\n" for name in ASSETS)
    (artifacts / "SHA256SUMS").write_text(sums)
    if case.missing_asset:
        (artifacts / case.missing_asset).unlink()
    for name, value in (("release", case.initial_release), ("tag", case.initial_tag), ("annotated-tag", case.annotated)):
        if value is not None:
            (state / (name + ".json")).write_text(json.dumps(value))
    plan = directory / "api-plan.json"
    plan.write_text(json.dumps(case.plan))
    curl_plan = directory / "curl-plan.json"
    curl_plan.write_text(json.dumps(case.curl_plan))
    output = directory / "github-output"
    output.write_text("")
    substitutions = {"{root}": str(directory), "{artifacts}": str(artifacts),
                     "{missing}": str(directory / "missing"), "{output}": str(output)}
    environment = {key: value for key, value in os.environ.items() if not key.startswith("FORGERULES_TEST_")}
    environment.update({"PATH": str(base / "bin") + os.pathsep + environment.get("PATH", ""),
                        "GH_TOKEN": "local-fixture-token", "GITHUB_OUTPUT": str(output),
                        "FORGERULES_TEST_ARTIFACTS": str(artifacts),
                        "FORGERULES_TEST_STATE_DIR": str(state), "FORGERULES_TEST_API_PLAN": str(plan),
                        "FORGERULES_TEST_CURL_PLAN": str(curl_plan)})
    environment.update({key: substitutions.get(value, value) for key, value in case.environment.items()})
    argv = ["bash", str(SCRIPTS / case.script)] + [substitutions.get(value, value) for value in case.args]
    # The program paths are repository scripts or copied fixtures, arguments
    # never pass through a shell, and the test token is synthetic.
    # Only case-selected repository scripts and synthetic local fixture operands are executed.
    # nosemgrep: python.lang.security.audit.dangerous-subprocess-use-audit.dangerous-subprocess-use-audit
    result = subprocess.run(argv, shell=False, cwd=str(directory), env=environment,  # nosec B603
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=60)
    log = state / "api.log"
    requests = log.read_text().splitlines() if log.exists() else []
    mutations = tuple(line for line in requests if line.startswith(("DELETE ", "PATCH ")))
    curl_log = state / "curl.log"
    curl_requests = tuple(curl_log.read_text().splitlines()) if curl_log.exists() else ()
    final = {}
    for name in ("release", "tag"):
        path = state / (name + ".json")
        final[name] = json.loads(path.read_text()) if path.exists() else None
    expected_release = case.initial_release if case.final_release is PRESERVE else case.final_release
    expected_tag = case.initial_tag if case.final_tag is PRESERVE else case.final_tag
    mismatches = []
    for name, actual, expected in (("exit", result.returncode, case.exit_status),
                                   ("mutations", mutations, case.mutations),
                                   ("release", final["release"], expected_release),
                                   ("tag", final["tag"], expected_tag),
                                   ("github_output", output.read_text(), case.github_output),
                                   ("curl_requests", curl_requests, case.curl_requests)):
        if actual != expected:
            mismatches.append("{}: got {!r}, expected {!r}".format(name, actual, expected))
    if case.diagnostic and case.diagnostic not in result.stderr:
        mismatches.append("missing diagnostic {!r}".format(case.diagnostic))
    if mismatches:
        raise AssertionError(case.name + "\n" + "\n".join(mismatches) + "\nAPI:\n" + "\n".join(requests)
                             + "\nSTDOUT:\n" + result.stdout + "\nSTDERR:\n" + result.stderr)
    return {"case": case.name, "status": result.returncode, "requests": requests,
            "mutations": list(mutations), "curl_requests": list(curl_requests),
            "github_output": output.read_text(), "release": final["release"],
            "tag": final["tag"], "stdout": result.stdout, "stderr": result.stderr}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--case", default="", help="run cases whose names contain this literal substring")
    parser.add_argument("--json-report", type=Path, help="write scenario outcomes for reproducible evidence")
    arguments = parser.parse_args()
    cases = (list(validation_cases()) + list(latest_cases()) + list(cleanup_cases())
             + list(patch_cases()) + list(verifier_cases()) + list(creator_cases()))
    cases = [case for case in cases if arguments.case in case.name]
    if not cases:
        parser.error("no matching release fault scenarios")
    failures = []
    outcomes = []
    with tempfile.TemporaryDirectory(prefix="forgerules-release-faults-") as temporary:
        base = Path(temporary)
        (base / "bin").mkdir()
        for name in ("gh", "curl"):
            destination = base / "bin" / name
            shutil.copyfile(SCRIPTS / "fixtures" / ("fake-" + name + "-release.sh"), destination)
            destination.chmod(0o755)
        for case in cases:
            try:
                outcomes.append(run_case(case, base))
            except (AssertionError, subprocess.TimeoutExpired) as failure:
                failures.append(str(failure))
                print(str(failure), file=sys.stderr)
    if arguments.json_report:
        arguments.json_report.parent.mkdir(parents=True, exist_ok=True)
        arguments.json_report.write_text(json.dumps({"cases": len(cases), "passed": len(outcomes),
                                                     "outcomes": outcomes, "failures": failures}, indent=2) + "\n")
    print("Release fault scenarios: {} passed, {} failed".format(len(outcomes), len(failures)))
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
