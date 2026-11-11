"""Tone reference for the vocal chain: the song's guide vocal or a built-in curve."""

import numpy as np

from services.guide import track_pitch
from services.vocal_chain import RATE, ltas_db
from services.vocal_curves import BUILTIN_LTAS

MAX_GUIDE_DISTANCE_ST = 7.0
MALE_BELOW_HZ = 165.0


def median_midi(x16: np.ndarray) -> float | None:
    """Median MIDI pitch of the voiced frames, None when nothing is voiced."""
    midi = track_pitch(x16, 16000, "m")
    voiced = midi[~np.isnan(midi)]
    if len(voiced) == 0:
        return None
    return float(np.median(voiced))


def choose_reference(
    takes16: np.ndarray, guide16: np.ndarray | None, guide44: np.ndarray | None
) -> tuple[str, np.ndarray]:
    """Return (kind, ref_ltas); kind is guide, builtin-male or builtin-female."""
    take_midi = median_midi(takes16)
    if guide16 is not None and guide44 is not None and take_midi is not None:
        guide_midi = median_midi(guide16)
        if (
            guide_midi is not None
            and abs(guide_midi - take_midi) <= MAX_GUIDE_DISTANCE_ST
        ):
            return "guide", ltas_db(guide44, RATE)
    if take_midi is None:
        kind = "builtin-male"
    else:
        hz = 440.0 * 2.0 ** ((take_midi - 69.0) / 12.0)
        kind = "builtin-male" if hz < MALE_BELOW_HZ else "builtin-female"
    return kind, BUILTIN_LTAS[kind]
