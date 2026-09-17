import hashlib, json, sys

REPO_SHA = "a0a6ae020bb3899ff0276067863e50523f897370"
ANNOTATOR = "claude-fable-5.1 (draft author; unreviewed)"
REVIEWER = "PENDING: independent reviewer has not confirmed this span"
PROVENANCE = "draft:2026-09-14:authored-against-pinned-checkout:unreviewed"


def fam(key):
    return "cobra-family-" + hashlib.sha256(("cobra-v3-draft:" + key).encode()).hexdigest()[:16]


# (stratum, query, path, start, end, anchor, reason, family_key)
Q = [
    # exact_identifier ---------------------------------------------------
    ("exact_identifier", "ValidateRequiredFlags", "command.go", 1146, 1168, "func (c *Command) ValidateRequiredFlags(",
     "The declaration of ValidateRequiredFlags itself.", "ValidateRequiredFlags"),
    ("exact_identifier", "Traverse", "command.go", 788, 829, "func (c *Command) Traverse(",
     "The declaration of Command.Traverse itself.", "Traverse"),
    ("exact_identifier", "MatchAll", "args.go", 113, 123, "func MatchAll(",
     "The declaration of MatchAll itself.", "MatchAll"),
    ("exact_identifier", "GetActiveHelpConfig", "active_help.go", 46, 57, "func GetActiveHelpConfig(",
     "The declaration of GetActiveHelpConfig itself.", "GetActiveHelpConfig"),
    ("exact_identifier", "RemoveCommand", "command.go", 1361, 1393, "func (c *Command) RemoveCommand(",
     "The declaration of Command.RemoveCommand itself.", "RemoveCommand"),
    ("exact_identifier", "IsAvailableCommand", "command.go", 1560, 1576, "func (c *Command) IsAvailableCommand(",
     "The declaration of Command.IsAvailableCommand itself.", "IsAvailableCommand"),
    ("exact_identifier", "CommandPath", "command.go", 1425, 1434, "func (c *Command) CommandPath(",
     "The declaration of Command.CommandPath itself.", "CommandPath"),
    ("exact_identifier", "GenYamlTreeCustom", "doc/yaml_docs.go", 59, 85, "func GenYamlTreeCustom(",
     "The declaration of GenYamlTreeCustom itself.", "GenYamlTreeCustom"),
    ("exact_identifier", "GetFlagCompletionFunc", "completions.go", 148, 160, "func (c *Command) GetFlagCompletionFunc(",
     "The declaration of Command.GetFlagCompletionFunc itself.", "GetFlagCompletionFunc"),
    ("exact_identifier", "AddTemplateFunc", "cobra.go", 83, 87, "func AddTemplateFunc(",
     "The declaration of AddTemplateFunc itself.", "AddTemplateFunc"),
    ("exact_identifier", "hasSeeAlso", "doc/util.go", 23, 37, "func hasSeeAlso(",
     "The declaration of hasSeeAlso itself.", "hasSeeAlso"),
    # exact_path ---------------------------------------------------------
    ("exact_path", "args.go", "args.go", 22, 39, "type PositionalArgs func(",
     "The file's defining type PositionalArgs and the legacyArgs default validator that opens it.", "path:args.go"),
    ("exact_path", "active_help.go", "active_help.go", 24, 33, "activeHelpMarker = ",
     "The file's defining constants: the active-help marker and environment variable names.", "path:active_help.go"),
    ("exact_path", "cobra.go", "cobra.go", 45, 66, "defaultPrefixMatching   = false",
     "The file's package-level behaviour toggles and their defaults.", "path:cobra.go"),
    ("exact_path", "completions.go", "completions.go", 26, 43, "ShellCompRequestCmd = ",
     "The file's defining constants and the ShellCompDirective type that the rest of the file implements.", "path:completions.go"),
    ("exact_path", "doc/md_docs.go", "doc/md_docs.go", 51, 54, "func GenMarkdown(",
     "The file's public entry point for one command; the tree variant delegates to the same custom generator.", "path:doc/md_docs.go"),
    ("exact_path", "doc/yaml_docs.go", "doc/yaml_docs.go", 30, 46, "type cmdOption struct",
     "The file's defining document types cmdOption and cmdDoc that every generator in it fills.", "path:doc/yaml_docs.go"),
    ("exact_path", "doc/rest_docs.go", "doc/rest_docs.go", 51, 59, "func defaultLinkHandler(",
     "The file's default link handler and its public single-command entry point.", "path:doc/rest_docs.go"),
    ("exact_path", "doc/util.go", "doc/util.go", 39, 46, "func forceMultiLine(",
     "One of the two helpers the file exists for; the other, hasSeeAlso, is judged separately.", "path:doc/util.go"),
    ("exact_path", "zsh_completions.go", "zsh_completions.go", 24, 44, "func (c *Command) GenZshCompletionFile(",
     "The file's four public entry points, with and without descriptions.", "path:zsh_completions.go"),
    ("exact_path", "fish_completions.go", "fish_completions.go", 275, 292, "func (c *Command) GenFishCompletion(",
     "The file's two public entry points.", "path:fish_completions.go"),
    ("exact_path", "powershell_completions.go", "powershell_completions.go", 305, 325, "func (c *Command) GenPowerShellCompletionFile(",
     "The file's four public entry points, with and without descriptions.", "path:powershell_completions.go"),
    # nl_behaviour -------------------------------------------------------
    ("nl_behaviour", "how does cobra warn that a command is deprecated when it runs", "command.go", 879, 881, "if len(c.Deprecated) > 0 {",
     "execute prints the deprecation notice before anything else runs.", "deprecation-notice"),
    ("nl_behaviour", "when does executing a command return flag.ErrHelp instead of running it", "command.go", 893, 925, "helpVal, err := c.Flags().GetBool(\"help\")",
     "The two ErrHelp returns: the help flag was set, or the command is not runnable.", "errhelp-return"),
    ("nl_behaviour", "how does cobra compare a typed command name with a subcommand name when case-insensitive matching is on", "command.go", 1876, 1885, "func commandNameMatches(",
     "commandNameMatches switches on EnableCaseInsensitive between EqualFold and ==.", "case-insensitive-match"),
    ("nl_behaviour", "what validation applies to positional arguments when the Args field is left nil", "command.go", 1139, 1144, "func (c *Command) ValidateArgs(",
     "ValidateArgs falls back to ArbitraryArgs when Args is nil.", "args-nil-default"),
    ("nl_behaviour", "how are deprecated-flag warnings printed after flags are parsed", "command.go", 1830, 1839, "err := c.Flags().Parse(args)",
     "ParseFlags prints the flag error buffer it grew during a successful parse.", "deprecated-flag-warning"),
    ("nl_behaviour", "how do the functions registered with OnInitialize get invoked during execution", "command.go", 1015, 1019, "func (c *Command) preRun(",
     "preRun runs every registered initializer; execute calls it before validating arguments.", "initializers-run"),
    ("nl_behaviour", "how does completion resolve a one-letter flag shorthand to its flag", "completions.go", 841, 858, "func findFlag(",
     "findFlag converts a shorthand to the long name through the local and inherited flag sets before Flag lookup.", "shorthand-lookup"),
    ("nl_behaviour", "why are required flags offered first when completing a flag name", "completions.go", 567, 590, "func completeRequireFlags(",
     "completeRequireFlags suggests only unset flags carrying the required annotation; regular flags follow only when it finds none.", "required-flags-first"),
    ("nl_behaviour", "how does the built-in help command complete the names of subcommands", "command.go", 1240, 1258, "ValidArgsFunction: func(c *Command, args []string, toComplete string)",
     "The help command's ValidArgsFunction finds the target command and lists its available subcommands by prefix.", "help-command-completion"),
    ("nl_behaviour", "how does completion stop when --help or --version is already on the command line", "completions.go", 522, 532, "func helpOrVersionFlagPresent(",
     "helpOrVersionFlagPresent detects the Cobra-added flags being set; getCompletions then returns no completions.", "help-version-stops-completion"),
    ("nl_behaviour", "how does cobra keep the list of subcommands sorted by name", "command.go", 1292, 1300, "func (c *Command) Commands(",
     "Commands sorts lazily by name unless EnableCommandSorting is off or the slice is already sorted.", "command-sorting"),
    # architecture_flow --------------------------------------------------
    ("architecture_flow", "in what order are persistent pre-run hooks collected from the parents before running a command", "command.go", 940, 966, "parents := make([]*Command, 0, 5)",
     "execute builds the parent list root-first under EnableTraverseRunHooks, otherwise nearest-first, then runs the first or all PersistentPreRun hooks.", "persistent-prerun-order"),
    ("architecture_flow", "how does a command find a flag by climbing the parent chain", "command.go", 1794, 1816, "func (c *Command) Flag(",
     "Flag tries the local set, then persistentFlag tries the command's persistent set and the merged parents' persistent flags.", "flag-lookup-chain"),
    ("architecture_flow", "how does the ReST documentation generator recurse over the command tree", "doc/rest_docs.go", 143, 170, "func GenReSTTreeCustom(",
     "GenReSTTreeCustom recurses into available non-help subcommands first, then writes the file for the current command.", "rest-tree-walk"),
    ("architecture_flow", "how does the default help command run help for a nested command path", "command.go", 1259, 1269, "Run: func(c *Command, args []string) {",
     "The help command's Run finds the target from the root, initialises its help and version flags, and calls Help.", "help-command-run"),
    ("architecture_flow", "how does Find recurse through subcommands while stripping flags", "command.go", 727, 741, "innerfind = func(c *Command, innerArgs []string)",
     "innerfind strips flags, takes the first remaining word as the next subcommand and recurses with that word removed.", "find-recursion"),
    ("architecture_flow", "how does flag group validation collect group status before checking each kind of group", "flag_groups.go", 79, 109, "func (c *Command) ValidateFlagGroups(",
     "ValidateFlagGroups visits every flag into three status maps, then validates required-together, one-required and mutually-exclusive groups in that order.", "flag-group-validation-flow"),
    ("architecture_flow", "how does completion fall back from subcommand names to ValidArgs and ArgAliases", "completions.go", 450, 494, "// Complete subcommand names, including the help command",
     "getCompletions lists subcommands, then required flags, then ValidArgs, and ArgAliases only when nothing matched.", "noun-completion-fallback"),
    ("architecture_flow", "how does getCompletions decide between the flag completion function and ValidArgsFunction", "completions.go", 502, 519, "var completionFn func(cmd *Command, args []string, toComplete string)",
     "A flag under completion selects its registered function; otherwise the command's ValidArgsFunction is called.", "completion-function-choice"),
    ("architecture_flow", "how does bash completion assemble the script from preamble, custom function and postscript", "bash_completions.go", 685, 697, "func (c *Command) GenBashCompletion(",
     "GenBashCompletion writes the preamble, the optional BashCompletionFunction, the generated commands and the postscript into one buffer.", "bash-script-assembly"),
    ("architecture_flow", "how does DebugFlags walk the command tree and label local versus persistent flags", "command.go", 1453, 1493, "func (c *Command) DebugFlags(",
     "DebugFlags recurses over subcommands, printing each flag with an L, LP or P label.", "debugflags-walk"),
    ("architecture_flow", "how does completion of subcommand names get suppressed once a local non-persistent flag is present", "completions.go", 436, 463, "foundLocalNonPersistentFlag := false",
     "getCompletions checks changed local non-persistent flags unless TraverseChildren is set, and skips subcommand completion when one is found.", "subcommand-completion-suppression"),
    # config_docs --------------------------------------------------------
    ("config_docs", "how do I limit a flag's completions to directory names", "site/content/completions/_index.md", 334, 347, "### Limit flag completions to directory names",
     "The documented MarkFlagDirname and ShellCompDirectiveFilterDirs options.", "flag-dirname-completion"),
    ("config_docs", "how do I remove the auto generated tag from generated documentation", "site/content/docgen/_index.md", 9, 13, "### `DisableAutoGenTag`",
     "The documented DisableAutoGenTag setting.", "disable-autogen-tag"),
    ("config_docs", "how can I make every parent's persistent hooks run instead of only the first", "site/content/user_guide.md", 690, 692, "By default, only the first persistent hook found in the command chain is executed.",
     "The documented EnableTraverseRunHooks switch.", "enable-traverse-run-hooks"),
    ("config_docs", "how do I customize the Error: prefix of printed errors", "site/content/user_guide.md", 599, 603, "## Error Message Prefix",
     "The documented SetErrPrefix function.", "error-prefix"),
    ("config_docs", "how do I turn off the descriptions shown next to shell completions", "site/content/completions/_index.md", 384, 395, "If you don't want to show descriptions in the completions",
     "The documented --no-descriptions flag of the default completion command.", "no-descriptions"),
    ("config_docs", "how do I make cobra parse local flags on parent commands", "site/content/user_guide.md", 288, 299, "### Local Flag on Parent Commands",
     "The documented TraverseChildren field.", "traverse-children-flags"),
    ("config_docs", "how do I attach a description to my own completion choices", "site/content/completions/_index.md", 373, 382, "Cobra allows you to add descriptions to your own completions.",
     "The documented tab-separated description convention for ValidArgs, ValidArgsFunction and flag completions.", "completion-descriptions"),
    ("config_docs", "how do I combine several positional argument validators", "site/content/user_guide.md", 384, 397, "Moreover, `MatchAll(pargs ...PositionalArgs)`",
     "The documented MatchAll combinator with its example.", "matchall-usage"),
    ("config_docs", "how do I stop cobra from sorting commands in the help output", "cobra.go", 57, 59, "var EnableCommandSorting = defaultCommandSorting",
     "The EnableCommandSorting variable and its doc comment.", "disable-command-sorting"),
    ("config_docs", "how do I provide my own usage template", "site/content/user_guide.md", 583, 590, "### Defining your own usage",
     "The documented SetUsageFunc and SetUsageTemplate methods.", "own-usage-template"),
    # ambiguous ----------------------------------------------------------
    ("ambiguous", "Root", "command.go", 860, 866, "func (c *Command) Root(",
     "Command.Root is the canonical definition; other hits are callers and 'root command' prose.", "amb:Root"),
    ("ambiguous", "Name", "command.go", 1495, 1503, "func (c *Command) Name(",
     "Command.Name is the canonical definition; the term also appears as struct fields and flag names.", "amb:Name"),
    ("ambiguous", "Parent", "command.go", 1842, 1845, "func (c *Command) Parent(",
     "Command.Parent is the canonical definition; the term also names fields, helpers and documentation.", "amb:Parent"),
    ("ambiguous", "Usage", "command.go", 443, 448, "func (c *Command) Usage(",
     "Command.Usage is the canonical definition among usage templates, functions and prose.", "amb:Usage"),
    ("ambiguous", "Help", "command.go", 470, 476, "func (c *Command) Help(",
     "Command.Help is the canonical definition among help templates, commands, flags and prose.", "amb:Help"),
    ("ambiguous", "Suggest", "command.go", 750, 765, "func (c *Command) findSuggestions(",
     "findSuggestions renders the suggestion block; SuggestFor and SuggestionsFor are neighbours.", "amb:Suggest"),
    ("ambiguous", "directive", "completions.go", 41, 43, "type ShellCompDirective int",
     "The ShellCompDirective type is the definition every directive constant and check refers to.", "amb:directive"),
    ("ambiguous", "template", "cobra.go", 32, 40, "var templateFuncs = template.FuncMap{",
     "templateFuncs is the definition behind every usage, help and version template.", "amb:template"),
    ("ambiguous", "Aliases", "command.go", 1505, 1513, "func (c *Command) HasAlias(",
     "HasAlias is the behaviour behind the Aliases field; the field itself is one line in the Command struct.", "amb:Aliases"),
    ("ambiguous", "prefix", "command.go", 1524, 1538, "func (c *Command) hasNameOrAliasPrefix(",
     "hasNameOrAliasPrefix is the prefix-matching implementation; EnablePrefixMatching is its switch.", "amb:prefix"),
]

want = {"exact_identifier": 11, "exact_path": 11, "nl_behaviour": 11, "architecture_flow": 11, "config_docs": 10, "ambiguous": 10}
counts = {}
queries = []
for i, (stratum, text, path, start, end, anchor, reason, key) in enumerate(Q, start=1):
    counts[stratum] = counts.get(stratum, 0) + 1
    queries.append({
        "id": "cd-%02d" % i,
        "stratum": stratum,
        "language": "en",
        "split": "dev",
        "query": text,
        "family_id": fam(key),
        "provenance": PROVENANCE,
        "judgements": [{
            "path": path, "start_line": start, "end_line": end, "anchor": anchor,
            "grade": 3, "reason": reason, "annotator": ANNOTATOR, "reviewer": REVIEWER,
        }],
    })
assert counts == want, counts
fams = [q["family_id"] for q in queries]
assert len(set(fams)) == len(fams), "duplicate family"
spans = [(q["judgements"][0]["path"], q["judgements"][0]["start_line"], q["judgements"][0]["end_line"]) for q in queries]
assert len(set(spans)) == len(spans), "duplicate span"

ds = {
    "schema_version": 1,
    "id": "cobra-v3-dev-draft",
    "repo": "cobra",
    "repo_sha": REPO_SHA,
    "language": "en",
    "evidence_class": "agent-drafted; NOT reviewed",
    "relevant_min_grade": 3,
    "notes": "DRAFT development split shaped like the sealed holdouts: 64 questions, 11/11/11/11/10/10 across the six answerable strata, exactly one small grade-3 span per question, no no_hit rows. Authored on 2026-09-14 against the pinned checkout by the candidate's author without access to any holdout question; every judgement is unreviewed and carries a PENDING reviewer. It must not enter a gate, a measurement or a release artifact until an independent reviewer has confirmed each span, replaced the reviewer field, and diffed the questions against the sealed holdout keys for collisions. See REVIEW.md beside this file.",
    "queries": queries,
}
out = sys.argv[1]
with open(out, "w") as f:
    json.dump(ds, f, indent=2)
    f.write("\n")
print("wrote", out, len(queries), counts)
