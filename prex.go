package main

// native imports
import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strconv"
	"strings"
	//"sync"
	"flag"
	"log"
)

// external imports
import (
	"github.com/mitchellh/go-homedir"
)

type Feature struct {
	reg          region
	feature      string
	strand       string
	geneID       string
	geneName     string
	transcriptID string
	appris       int
}

type region struct {
	chrom  string
	start  int
	end    int
	strand string
}

//methods bound to the region struct

// is the region totally uninitialized?
func (r region) isEmpty() bool {
	if r.start == 0 && r.end == 0 && r.chrom == "" && r.strand == "" {
		return true
	}
	return false
}

// is the region "greater than" the passed region?
// BUG(karl) I am not sure this is complete?
func (r region) greaterThan(inR region) bool {
	//if r's region is bigger than inR's region, return true
	if r.chrom == inR.chrom && r.strand == inR.strand {
		if r.start < inR.start {
			return true
		} else if r.end > inR.end {
			return true
		}
	} else if inR.isEmpty() {
		// if the checked region is empty, r has to be bigger
		return true
	}
	return false
}

// take the union of r and inR to create the largest possible region
func (r region) expandTo(inR region) region {
	// take the union of r and inR to create the largest possible region
	if r.chrom == inR.chrom && r.strand == inR.strand {
		if r.start > inR.start {
			r.start = inR.start
		} else if r.end < inR.end {
			r.end = inR.end
		}
	} else if r.start == 0 && r.end == 0 {
		r = inR
	}
	return r
}

// parse GFF3 row
// http://www.sequenceontology.org/gff3.shtml
// tags below really means column 9, "attributes"
func getGene(line string) Feature {
	spl := strings.Split(line, "\t")

	feat := spl[2]

	reg := region{chrom: spl[0], start: mustAtoi(spl[3]), end: mustAtoi(spl[4]), strand: spl[6]}

	geneID := ""
	transcriptID := ""
	geneName := ""
	appris := 0
	tags := strings.Split(spl[8], ";")
	for _, v := range tags {
		tag := strings.Split(v, "=")
		if tag[0] == "gene_id" {
			geneID = tag[1]
		} else if tag[0] == "transcript_id" {
			transcriptID = tag[1]
		} else if tag[0] == "gene_name" {
			geneName = tag[1]
		} else if strings.Contains(tag[1], "appris_principal") {
			for _, field := range strings.Split(tag[1], ",") {
				if strings.Contains(field, "appris_principal") {
					appris, _ = strconv.Atoi(strings.Split(field, "_")[2])
				}
			}

		}
	}
	thisFeat := Feature{
		reg:          reg,
		feature:      feat,
		geneName:     geneName,
		geneID:       geneID,
		transcriptID: transcriptID,
		appris:       appris,
	}
	return thisFeat
}

func appendIfNew(refList []region, addition region) []region {
	for _, v := range refList {
		if v == addition {
			return refList
		}
	}
	return append(refList, addition)
}

func expandIfNew(runningRegion, addition region) region {
	if addition.greaterThan(runningRegion) {
		return runningRegion.expandTo(addition)
	} else {
		return runningRegion
	}
}

func anyIn(inGenes []string, inString string) bool {
	// are any of our genes of interest in this string?
	for _, b := range inGenes {
		if strings.Contains(inString, b) {
			return true
		}
	}
	return false
}

// readGzFile reads a specified gzipped Gff3 file,
// (uncompressed Gff3 files are not supported at this time)
// and returns a map of symbols (gene names) to region definitions
func readGzFile(filename string, inGenes []string) (map[string]region, error) {
	// function to read gzipped files
	fi, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer fi.Close()
	fz, err := gzip.NewReader(fi)
	// TODO: detect file format (i.e. .gz) and dynamically open or gzip.open as necessary
	if err != nil {
		return nil, err
	}
	defer fz.Close()
	scanner := bufio.NewScanner(fz)
	out := make(map[string]region)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "#") && anyIn(inGenes, line) {
			thisFeat := getGene(line)
			if thisFeat.appris > 0 { // && (thisFeat.feature == "start_codon") {
				// add redundant copies of this feature to the map with
				// gene_id, transcript_id, and gene_name keys
				// TODO: only add the features that were detected?
				//       make a channel for each feature type?
				//	     maybe use pointers to avoid duplicating data in memory?
				//          I don't think pointers will work, since we don't know which elements of the array will be the same
				tfr := thisFeat.reg // tfr stands for "this feature region"
				out[thisFeat.geneID] = expandIfNew(out[thisFeat.geneID], tfr)
				out[thisFeat.transcriptID] = expandIfNew(out[thisFeat.transcriptID], tfr)
				out[thisFeat.geneName] = expandIfNew(out[thisFeat.geneName], tfr)
			}
		}
	}
	return out, nil
}

// validateID takes a lookup table , f (from readGzFile); and an identifier
// (i.e. gene name), v. It returns true if that id was found in the
// annotation, or false if not
func validateID(f map[string]region, v string) bool {
	if _, ok := f[v]; ok {
		return true
	} else {
		warn("invalid or unknown identifier: " + v)
		return false
	}
	/*
		    resCount := f[v]
			if resCount == 0 {
				warn("nothing found for " + v)
				return false
			} else if resCount > 1 {
				warn("too many primary isoforms for " + v)
				//fmt.Println(f[v])
				return false
			}
			return true
	*/
}

// expandRegion takes a region, r; and a number of nucleotides
// upstream and downstream by which to expand the region def'n
// it returns the expanded region
func expandRegion(r region, up int, down int) region {
	bedStart := 0
	bedEnd := 0
	if r.strand == "+" {
		bedStart = r.start - up
		bedEnd = r.start + down
	} else if r.strand == "-" {
		bedStart = r.end - down
		bedEnd = r.end + up
	} else {
		fmt.Println(r)
		warn("no strand found!")
	}
	out := region{chrom: r.chrom, start: bedStart, end: bedEnd, strand: r.strand}
	return out
}

// doBedStuff builds a temporary BED file containing the region of interest
// and executes bedtools' getfasta command
func doBedStuff(r region, fastaIn string, fastaOut string, name string) {
	log.Println("doBedStuff() name: " + name)
	log.Println("doBedStuff() r.start: " + strconv.Itoa(r.start))
	tempDir := os.TempDir()
	tempFile, err := ioutil.TempFile(tempDir, "prex_")
	if err != nil {
		abort(err)
	}
	defer os.Remove(tempFile.Name())

	bedName := name + ";" + r.chrom + ":" + strconv.Itoa(r.start) + "-" + strconv.Itoa(r.end) + "(" + r.strand + ")"
	bedString := strings.Join([]string{r.chrom, strconv.Itoa(r.start), strconv.Itoa(r.end), bedName, ".", r.strand}, "\t")
	err = ioutil.WriteFile(tempFile.Name(), []byte(bedString+"\n"), 600)
	if err != nil {
		abort(err)
	}
	_, err = exec.Command("bedtools", "getfasta", "-name", "-s", "-fi", fastaIn, "-bed", tempFile.Name(), "-fo", fastaOut).Output()
	if err != nil {
		abort(err)
	}
	//wg.Done()
}

func loadConfig() map[string]string {
	var config struct {
		Gff3  string
		Fasta string
	}
	file, err := os.Open("./prex.json")
	if err != nil {
		abort(err)
	}
	jsonParser := json.NewDecoder(file)
	if err = jsonParser.Decode(&config); err != nil {
		abort(err)
	}
	config.Fasta, _ = homedir.Expand(config.Fasta)
	config.Gff3, _ = homedir.Expand(config.Gff3)
	return map[string]string{"fasta": config.Fasta, "gff3": config.Gff3}
}

//var wg sync.WaitGroup

func main() {
	flagGff3 := flag.String("gff3", "", "gtf annotation")
	flagFasta := flag.String("fasta", "", "fasta sequence file")
	flagUp := flag.Int("up", 0, "upstream distance")
	flagDown := flag.Int("down", 0, "downstream distance")
	flag.Parse()
	inGenes := flag.Args()

	if len(inGenes) < 1 {
		warn("No arguments found! Pass some feature names!")
		os.Exit(1)
	} else if len(inGenes) == 1 {
		// is this a file?
		fi, err := os.Open(inGenes[0])
		if err == nil {
			// this appears to be a file
			defer fi.Close()
			var geneFileGenes []string
			scanner := bufio.NewScanner(fi)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.TrimSpace(line) != "" {
					geneFileGenes = append(geneFileGenes, line)
				}
				inGenes = geneFileGenes
			}
		}
		// otherwise, assume this is a gene identifier
	}

	if *flagUp == 0 && *flagDown == 0 {
		warn("Must define upstream and/or downstream")
	}

	config := loadConfig()
	fasta := config["fasta"]
	if *flagFasta != "" {
		// if command line flag is provided, take it
		fasta = *flagFasta
	}
	if _, err := os.Stat(fasta); err != nil {
		abort(err)
	}
	gff3 := config["gff3"]
	if *flagGff3 != "" {
		// if command line flag is provided, take it
		gff3 = *flagGff3
	}
	info("reading " + gff3)
	f, err := readGzFile(gff3, inGenes)
	if err != nil {
		abort(err)
	}
	info("probing genes ...")
	//inGenes := []string{"GATA2", "DNMT3A","RUNX1","ASXL1","MADE_UP_GENE"}

	for _, v := range inGenes {
		if validateID(f, v) {
			//wg.Add(1)
			info(v)
			outFasta := strings.Join([]string{v, "fa"}, ".")
			doBedStuff(expandRegion(f[v], *flagDown, *flagUp), fasta, outFasta, v)
			info("\tdone!")
			fmt.Println()
		} else {
			fmt.Println()
		}
	}
	//wg.Wait()
}

func mustAtoi(s string) int {
	i, err := strconv.ParseInt(s, 0, 0)
	if err != nil {
		warn("mustAtoi() " + s)
		abort(err)
	}
	return int(i)
}

func info(message string) {
	log.Println("[ok] " + message)
}

func warn(message string) {
	log.Println("[* ] " + message)
}

func abort(err error) {
	log.Fatalln("[!!] " + err.Error())
}
