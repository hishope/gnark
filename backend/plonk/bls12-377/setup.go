// Copyright 2020 ConsenSys Software Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Code generated by gnark DO NOT EDIT

package plonk

import (
	"errors"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-377/fr"
	"github.com/consensys/gnark-crypto/ecc/bls12-377/fr/fft"
	"github.com/consensys/gnark-crypto/ecc/bls12-377/fr/iop"
	"github.com/consensys/gnark-crypto/ecc/bls12-377/fr/kzg"
	"github.com/consensys/gnark/backend/plonk/internal"
	"github.com/consensys/gnark/constraint/bls12-377"
)

// Trace stores a plonk trace as columns
type Trace struct {

	// Constants describing a plonk circuit. The first entries
	// of LQk (whose index correspond to the public inputs) are set to 0, and are to be
	// completed by the prover. At those indices i (so from 0 to nb_public_variables), LQl[i]=-1
	// so the first nb_public_variables constraints look like this:
	// -1*Wire[i] + 0* + 0 . It is zero when the constant coefficient is replaced by Wire[i].
	Ql, Qr, Qm, Qo, Qk *iop.Polynomial
	Qcp                []*iop.Polynomial

	// Polynomials representing the splitted permutation. The full permutation's support is 3*N where N=nb wires.
	// The set of interpolation is <g> of size N, so to represent the permutation S we let S acts on the
	// set A=(<g>, u*<g>, u^{2}*<g>) of size 3*N, where u is outside <g> (its use is to shift the set <g>).
	// We obtain a permutation of A, A'. We split A' in 3 (A'_{1}, A'_{2}, A'_{3}), and S1, S2, S3 are
	// respectively the interpolation of A'_{1}, A'_{2}, A'_{3} on <g>.
	S1, S2, S3 *iop.Polynomial

	// S full permutation, i -> S[i]
	S []int64
}

// VerifyingKey stores the data needed to verify a proof:
// * The commitment scheme
// * Commitments of ql prepended with as many ones as there are public inputs
// * Commitments of qr, qm, qo, qk prepended with as many zeroes as there are public inputs
// * Commitments to S1, S2, S3
type VerifyingKey struct {

	// Size circuit
	Size              uint64
	SizeInv           fr.Element
	Generator         fr.Element
	NbPublicVariables uint64

	// Commitment scheme that is used for an instantiation of PLONK
	Kzg kzg.VerifyingKey

	// cosetShift generator of the coset on the small domain
	CosetShift fr.Element

	// S commitments to S1, S2, S3
	S [3]kzg.Digest

	// Commitments to ql, qr, qm, qo, qcp prepended with as many zeroes (ones for l) as there are public inputs.
	// In particular Qk is not complete.
	Ql, Qr, Qm, Qo, Qk kzg.Digest
	Qcp                []kzg.Digest

	CommitmentConstraintIndexes []uint64
}

// ProvingKey stores the data needed to generate a proof:
// * the commitment scheme
// * ql, prepended with as many ones as they are public inputs
// * qr, qm, qo prepended with as many zeroes as there are public inputs.
// * qk, prepended with as many zeroes as public inputs, to be completed by the prover
// with the list of public inputs.
// * sigma_1, sigma_2, sigma_3 in both basis
// * the copy constraint permutation
type ProvingKey struct {

	// stores ql, qr, qm, qo, qk (-> to be completed by the prover)
	// and s1, s2, s3. They are set in canonical basis before generating the proof, they will be used
	// for computing the opening proofs (hence the canonical form). The canonical version
	// of qk incomplete is used in the linearisation polynomial.
	// The polynomials in trace are in canonical basis.
	trace Trace

	Kzg kzg.ProvingKey

	// Verifying Key is embedded into the proving key (needed by Prove)
	Vk *VerifyingKey

	// qr,ql,qm,qo,qcp in LagrangeCoset --> these are not serialized, but computed from Ql, Qr, Qm, Qo, Qcp once.
	lcQl, lcQr, lcQm, lcQo *iop.Polynomial
	lcQcp                  []*iop.Polynomial

	// LQk qk in Lagrange form -> to be completed by the prover. After being completed,
	lQk *iop.Polynomial

	// Domains used for the FFTs.
	// Domain[0] = small Domain
	// Domain[1] = big Domain
	Domain [2]fft.Domain

	// in lagrange coset basis --> these are not serialized, but computed from S1Canonical, S2Canonical, S3Canonical once.
	lcS1, lcS2, lcS3 *iop.Polynomial

	// in lagrange coset basis --> not serialized id and L_{g^{0}}
	lcIdIOP, lLoneIOP *iop.Polynomial
}

func Setup(spr *cs.SparseR1CS, kzgSrs kzg.SRS) (*ProvingKey, *VerifyingKey, error) {

	var pk ProvingKey
	var vk VerifyingKey
	pk.Vk = &vk
	vk.CommitmentConstraintIndexes = internal.IntSliceToUint64Slice(spr.CommitmentInfo.CommitmentWireIndexes())
	// nbConstraints := len(spr.Constraints)

	// step 0: set the fft domains
	pk.initDomains(spr)

	// step 1: set the verifying key
	pk.Vk.CosetShift.Set(&pk.Domain[0].FrMultiplicativeGen)
	vk.Size = pk.Domain[0].Cardinality
	vk.SizeInv.SetUint64(vk.Size).Inverse(&vk.SizeInv)
	vk.Generator.Set(&pk.Domain[0].Generator)
	vk.NbPublicVariables = uint64(len(spr.Public))
	if len(kzgSrs.Pk.G1) < int(vk.Size) {
		return nil, nil, errors.New("kzg srs is too small")
	}
	pk.Kzg = kzgSrs.Pk
	vk.Kzg = kzgSrs.Vk

	// step 2: ql, qr, qm, qo, qk, qcp in Lagrange Basis
	BuildTrace(spr, &pk.trace)

	// step 3: build the permutation and build the polynomials S1, S2, S3 to encode the permutation.
	// Note: at this stage, the permutation takes in account the placeholders
	nbVariables := spr.NbInternalVariables + len(spr.Public) + len(spr.Secret)
	buildPermutation(spr, &pk.trace, nbVariables)
	s := computePermutationPolynomials(&pk.trace, &pk.Domain[0])
	pk.trace.S1 = s[0]
	pk.trace.S2 = s[1]
	pk.trace.S3 = s[2]

	// step 4: commit to s1, s2, s3, ql, qr, qm, qo, and (the incomplete version of) qk.
	// All the above polynomials are expressed in canonical basis afterwards. This is why
	// we save lqk before, because the prover needs to complete it in Lagrange form, and
	// then express it on the Lagrange coset basis.
	pk.lQk = pk.trace.Qk.Clone() // it will be completed by the prover, and the evaluated on the coset
	err := commitTrace(&pk.trace, &pk)
	if err != nil {
		return nil, nil, err
	}

	// step 5: evaluate ql, qr, qm, qo, s1, s2, s3 on LagrangeCoset (NOT qk)
	// we clone them, because the canonical versions are going to be used in
	// the opening proof
	pk.computeLagrangeCosetPolys()

	return &pk, &vk, nil
}

// computeLagrangeCosetPolys computes each polynomial except qk in Lagrange coset
// basis. Qk will be evaluated in Lagrange coset basis once it is completed by the prover.
func (pk *ProvingKey) computeLagrangeCosetPolys() {
	pk.lcQcp = make([]*iop.Polynomial, len(pk.trace.Qcp))
	for i, qcpI := range pk.trace.Qcp {
		pk.lcQcp[i] = qcpI.Clone().ToLagrangeCoset(&pk.Domain[1])
	}
	pk.lcQl = pk.trace.Ql.Clone().ToLagrangeCoset(&pk.Domain[1])
	pk.lcQr = pk.trace.Qr.Clone().ToLagrangeCoset(&pk.Domain[1])
	pk.lcQm = pk.trace.Qm.Clone().ToLagrangeCoset(&pk.Domain[1])
	pk.lcQo = pk.trace.Qo.Clone().ToLagrangeCoset(&pk.Domain[1])
	pk.lcS1 = pk.trace.S1.Clone().ToLagrangeCoset(&pk.Domain[1])
	pk.lcS2 = pk.trace.S2.Clone().ToLagrangeCoset(&pk.Domain[1])
	pk.lcS3 = pk.trace.S3.Clone().ToLagrangeCoset(&pk.Domain[1])

	// storing Id
	lagReg := iop.Form{Basis: iop.Lagrange, Layout: iop.Regular}
	id := make([]fr.Element, pk.Domain[1].Cardinality)
	id[0].Set(&pk.Domain[1].FrMultiplicativeGen)
	for i := 1; i < int(pk.Domain[1].Cardinality); i++ {
		id[i].Mul(&id[i-1], &pk.Domain[1].Generator)
	}
	pk.lcIdIOP = iop.NewPolynomial(&id, lagReg)

	// L_{g^{0}}
	cap := pk.Domain[1].Cardinality
	if cap < pk.Domain[0].Cardinality {
		cap = pk.Domain[0].Cardinality // sanity check
	}
	lone := make([]fr.Element, pk.Domain[0].Cardinality, cap)
	lone[0].SetOne()
	pk.lLoneIOP = iop.NewPolynomial(&lone, lagReg).ToCanonical(&pk.Domain[0]).
		ToRegular().
		ToLagrangeCoset(&pk.Domain[1])
}

// NbPublicWitness returns the expected public witness size (number of field elements)
func (vk *VerifyingKey) NbPublicWitness() int {
	return int(vk.NbPublicVariables)
}

// VerifyingKey returns pk.Vk
func (pk *ProvingKey) VerifyingKey() interface{} {
	return pk.Vk
}

// BuildTrace fills the constant columns ql, qr, qm, qo, qk from the sparser1cs.
// Size is the size of the system that is nb_constraints+nb_public_variables
func BuildTrace(spr *cs.SparseR1CS, pt *Trace) {

	nbConstraints := spr.GetNbConstraints()
	sizeSystem := uint64(nbConstraints + len(spr.Public))
	size := ecc.NextPowerOfTwo(sizeSystem)

	ql := make([]fr.Element, size)
	qr := make([]fr.Element, size)
	qm := make([]fr.Element, size)
	qo := make([]fr.Element, size)
	qk := make([]fr.Element, size)
	qcp := make([][]fr.Element, len(spr.CommitmentInfo))

	for i := 0; i < len(spr.Public); i++ { // placeholders (-PUB_INPUT_i + qk_i = 0) TODO should return error is size is inconsistent
		ql[i].SetOne().Neg(&ql[i])
		qr[i].SetZero()
		qm[i].SetZero()
		qo[i].SetZero()
		qk[i].SetZero() // → to be completed by the prover
	}
	offset := len(spr.Public)

	j := 0
	it := spr.GetSparseR1CIterator()
	for c := it.Next(); c != nil; c = it.Next() {
		ql[offset+j].Set(&spr.Coefficients[c.QL])
		qr[offset+j].Set(&spr.Coefficients[c.QR])
		qm[offset+j].Set(&spr.Coefficients[c.QM])
		qo[offset+j].Set(&spr.Coefficients[c.QO])
		qk[offset+j].Set(&spr.Coefficients[c.QC])
		j++
	}

	lagReg := iop.Form{Basis: iop.Lagrange, Layout: iop.Regular}

	pt.Ql = iop.NewPolynomial(&ql, lagReg)
	pt.Qr = iop.NewPolynomial(&qr, lagReg)
	pt.Qm = iop.NewPolynomial(&qm, lagReg)
	pt.Qo = iop.NewPolynomial(&qo, lagReg)
	pt.Qk = iop.NewPolynomial(&qk, lagReg)
	pt.Qcp = make([]*iop.Polynomial, len(qcp))

	for i := range spr.CommitmentInfo {
		qcp[i] = make([]fr.Element, size)
		for _, committed := range spr.CommitmentInfo[i].Committed {
			qcp[i][offset+committed].SetOne()
		}
		pt.Qcp[i] = iop.NewPolynomial(&qcp[i], lagReg)
	}
}

// commitTrace commits to every polynomial in the trace, and put
// the commitments int the verifying key.
func commitTrace(trace *Trace, pk *ProvingKey) error {

	trace.Ql.ToCanonical(&pk.Domain[0]).ToRegular()
	trace.Qr.ToCanonical(&pk.Domain[0]).ToRegular()
	trace.Qm.ToCanonical(&pk.Domain[0]).ToRegular()
	trace.Qo.ToCanonical(&pk.Domain[0]).ToRegular()
	trace.Qk.ToCanonical(&pk.Domain[0]).ToRegular() // -> qk is not complete
	trace.S1.ToCanonical(&pk.Domain[0]).ToRegular()
	trace.S2.ToCanonical(&pk.Domain[0]).ToRegular()
	trace.S3.ToCanonical(&pk.Domain[0]).ToRegular()

	var err error
	pk.Vk.Qcp = make([]kzg.Digest, len(trace.Qcp))
	for i := range trace.Qcp {
		trace.Qcp[i].ToCanonical(&pk.Domain[0]).ToRegular()
		if pk.Vk.Qcp[i], err = kzg.Commit(pk.trace.Qcp[i].Coefficients(), pk.Kzg); err != nil {
			return err
		}
	}
	if pk.Vk.Ql, err = kzg.Commit(pk.trace.Ql.Coefficients(), pk.Kzg); err != nil {
		return err
	}
	if pk.Vk.Qr, err = kzg.Commit(pk.trace.Qr.Coefficients(), pk.Kzg); err != nil {
		return err
	}
	if pk.Vk.Qm, err = kzg.Commit(pk.trace.Qm.Coefficients(), pk.Kzg); err != nil {
		return err
	}
	if pk.Vk.Qo, err = kzg.Commit(pk.trace.Qo.Coefficients(), pk.Kzg); err != nil {
		return err
	}
	if pk.Vk.Qk, err = kzg.Commit(pk.trace.Qk.Coefficients(), pk.Kzg); err != nil {
		return err
	}
	if pk.Vk.S[0], err = kzg.Commit(pk.trace.S1.Coefficients(), pk.Kzg); err != nil {
		return err
	}
	if pk.Vk.S[1], err = kzg.Commit(pk.trace.S2.Coefficients(), pk.Kzg); err != nil {
		return err
	}
	if pk.Vk.S[2], err = kzg.Commit(pk.trace.S3.Coefficients(), pk.Kzg); err != nil {
		return err
	}
	return nil
}

func (pk *ProvingKey) initDomains(spr *cs.SparseR1CS) {

	nbConstraints := spr.GetNbConstraints()
	sizeSystem := uint64(nbConstraints + len(spr.Public)) // len(spr.Public) is for the placeholder constraints
	pk.Domain[0] = *fft.NewDomain(sizeSystem)

	// h, the quotient polynomial is of degree 3(n+1)+2, so it's in a 3(n+2) dim vector space,
	// the domain is the next power of 2 superior to 3(n+2). 4*domainNum is enough in all cases
	// except when n<6.
	if sizeSystem < 6 {
		pk.Domain[1] = *fft.NewDomain(8 * sizeSystem)
	} else {
		pk.Domain[1] = *fft.NewDomain(4 * sizeSystem)
	}

}

// buildPermutation builds the Permutation associated with a circuit.
//
// The permutation s is composed of cycles of maximum length such that
//
//	s. (l∥r∥o) = (l∥r∥o)
//
// , where l∥r∥o is the concatenation of the indices of l, r, o in
// ql.l+qr.r+qm.l.r+qo.O+k = 0.
//
// The permutation is encoded as a slice s of size 3*size(l), where the
// i-th entry of l∥r∥o is sent to the s[i]-th entry, so it acts on a tab
// like this: for i in tab: tab[i] = tab[permutation[i]]
func buildPermutation(spr *cs.SparseR1CS, pt *Trace, nbVariables int) {

	// nbVariables := spr.NbInternalVariables + len(spr.Public) + len(spr.Secret)
	sizeSolution := len(pt.Ql.Coefficients())
	sizePermutation := 3 * sizeSolution

	// init permutation
	permutation := make([]int64, sizePermutation)
	for i := 0; i < len(permutation); i++ {
		permutation[i] = -1
	}

	// init LRO position -> variable_ID
	lro := make([]int, sizePermutation) // position -> variable_ID
	for i := 0; i < len(spr.Public); i++ {
		lro[i] = i // IDs of LRO associated to placeholders (only L needs to be taken care of)
	}

	offset := len(spr.Public)

	j := 0
	it := spr.GetSparseR1CIterator()
	for c := it.Next(); c != nil; c = it.Next() {
		lro[offset+j] = int(c.XA)
		lro[sizeSolution+offset+j] = int(c.XB)
		lro[2*sizeSolution+offset+j] = int(c.XC)

		j++
	}

	// init cycle:
	// map ID -> last position the ID was seen
	cycle := make([]int64, nbVariables)
	for i := 0; i < len(cycle); i++ {
		cycle[i] = -1
	}

	for i := 0; i < len(lro); i++ {
		if cycle[lro[i]] != -1 {
			// if != -1, it means we already encountered this value
			// so we need to set the corresponding permutation index.
			permutation[i] = cycle[lro[i]]
		}
		cycle[lro[i]] = int64(i)
	}

	// complete the Permutation by filling the first IDs encountered
	for i := 0; i < sizePermutation; i++ {
		if permutation[i] == -1 {
			permutation[i] = cycle[lro[i]]
		}
	}

	pt.S = permutation
}

// computePermutationPolynomials computes the LDE (Lagrange basis) of the permutation.
// We let the permutation act on <g> || u<g> || u^{2}<g>, split the result in 3 parts,
// and interpolate each of the 3 parts on <g>.
func computePermutationPolynomials(pt *Trace, domain *fft.Domain) [3]*iop.Polynomial {

	nbElmts := int(domain.Cardinality)

	var res [3]*iop.Polynomial

	// Lagrange form of ID
	evaluationIDSmallDomain := getSupportPermutation(domain)

	// Lagrange form of S1, S2, S3
	s1Canonical := make([]fr.Element, nbElmts)
	s2Canonical := make([]fr.Element, nbElmts)
	s3Canonical := make([]fr.Element, nbElmts)
	for i := 0; i < nbElmts; i++ {
		s1Canonical[i].Set(&evaluationIDSmallDomain[pt.S[i]])
		s2Canonical[i].Set(&evaluationIDSmallDomain[pt.S[nbElmts+i]])
		s3Canonical[i].Set(&evaluationIDSmallDomain[pt.S[2*nbElmts+i]])
	}

	lagReg := iop.Form{Basis: iop.Lagrange, Layout: iop.Regular}
	res[0] = iop.NewPolynomial(&s1Canonical, lagReg)
	res[1] = iop.NewPolynomial(&s2Canonical, lagReg)
	res[2] = iop.NewPolynomial(&s3Canonical, lagReg)

	return res
}

// getSupportPermutation returns the support on which the permutation acts, it is
// <g> || u<g> || u^{2}<g>
func getSupportPermutation(domain *fft.Domain) []fr.Element {

	res := make([]fr.Element, 3*domain.Cardinality)

	res[0].SetOne()
	res[domain.Cardinality].Set(&domain.FrMultiplicativeGen)
	res[2*domain.Cardinality].Square(&domain.FrMultiplicativeGen)

	for i := uint64(1); i < domain.Cardinality; i++ {
		res[i].Mul(&res[i-1], &domain.Generator)
		res[domain.Cardinality+i].Mul(&res[domain.Cardinality+i-1], &domain.Generator)
		res[2*domain.Cardinality+i].Mul(&res[2*domain.Cardinality+i-1], &domain.Generator)
	}

	return res
}
