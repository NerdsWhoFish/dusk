import { useEffect, useState } from "react";

import { api } from "./api";
import type { KindCount, KindInfo, Vocabulary } from "./api";

const nothing: Vocabulary = { roles: [], kinds: [] };

export function useVocabulary(): Vocabulary {
  const [vocabulary, setVocabulary] = useState<Vocabulary>(nothing);

  useEffect(() => {
    let live = true;
    // A vocabulary that cannot be read costs the roles, never the page: the
    // kinds are still counted and still filter.
    void api.kinds({ onFresh: (answer) => live && setVocabulary(answer) }).catch(() => nothing).then((answer) => {
      if (live) {
        setVocabulary(answer);
      }
    });
    return () => {
      live = false;
    };
  }, []);

  return vocabulary;
}

export function describe(
  kind: string,
  vocabulary: Vocabulary,
): KindInfo | undefined {
  return vocabulary.kinds.find((entry) => entry.kind === kind);
}

// Chip is one kind as the landing page filters by it. The kind is the literal
// one, so clicking a chip asks for exactly what the chip says.
export type Chip = {
  kind: string;
  count: number;
  aliases: string[];
  aliasOf?: string;
};

export type KindGroup = { role: string; chips: Chip[] };

// group splits counted kinds by role, in the order the server ranked them. One
// role means one unlabelled group, which is what the page rendered before roles
// existed: not minting changes nothing here as well as in drift (ADR-0048).
export function group(kinds: KindCount[], vocabulary: Vocabulary): KindGroup[] {
  if (kinds.length === 0) {
    return [];
  }

  const groups: KindGroup[] = [];
  const placed = new Set<string>();

  for (const role of vocabulary.roles) {
    const chips = kinds
      .filter((counted) => describe(counted.Kind, vocabulary)?.role === role)
      .map((counted) => chip(counted, vocabulary));
    if (chips.length === 0) {
      continue;
    }
    for (const one of chips) {
      placed.add(one.kind);
    }
    groups.push({ role, chips });
  }

  // A kind the vocabulary said nothing about is still shown. Dropping it would
  // hide entities from the one control that lists them.
  const rest = kinds.filter((counted) => !placed.has(counted.Kind));
  if (rest.length > 0) {
    groups.push({ role: "", chips: rest.map((counted) => chip(counted, vocabulary)) });
  }

  if (groups.length > 1) {
    return groups;
  }
  return [{ role: "", chips: kinds.map((counted) => chip(counted, vocabulary)) }];
}

function chip(counted: KindCount, vocabulary: Vocabulary): Chip {
  const known = describe(counted.Kind, vocabulary);
  return {
    kind: counted.Kind,
    count: counted.Count,
    aliases: known?.aliases ?? [],
    aliasOf: known?.alias_of,
  };
}
