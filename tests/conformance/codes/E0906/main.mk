// verdict: reject E0906
// note: a gen argument that names a type only generated code of the module defines
import gens

gen gens.Make()

gen gens.Look(Made) // <- E0906
